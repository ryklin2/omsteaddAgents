package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// =============================================================================
// Live McDVoice submission.
//
// Enabled only when MCDVOICE_LIVE=1. Everything here performs real, irreversible
// submissions against a third-party survey, consuming the single use of a real
// receipt code, so it is off by default.
//
// WHAT IS VERIFIED AND WHAT IS NOT
//
// The entry form was read from the live site, so step 1 is written against
// observed reality: a plain POST form (no ASP.NET __VIEWSTATE), fields CN1-CN5
// at five digits each plus CN6 at one, alongside JavaScriptEnabled, FIP,
// AllowCapture and NextButton.
//
// The question pages AFTER that entry step were never observed, because
// reaching them requires a real unused receipt code. So the walker below is
// generic: rather than hardcoding a question sequence that would be a guess, it
// parses whatever form each page serves and answers it by inspecting the
// controls it finds. Expect to adjust answerPage once you see a real
// transcript. Set MCDVOICE_DEBUG=1 to dump each page's controls.
// =============================================================================

const (
	mcdVoiceEntryURL = "https://www.mcdvoice.com/"

	// browserUA is sent because SMG's survey engine serves a degraded or
	// blocked page to obvious non-browser clients.
	browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	// maxSurveySteps bounds the walk. A survey that loops — because a page
	// rejects its own answers and re-serves itself — would otherwise submit
	// forever.
	maxSurveySteps = 40
)

// liveEnabled reports whether real submission is turned on.
func liveEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MCDVOICE_LIVE"))
	return v == "1" || strings.EqualFold(v, "true")
}

func debugEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MCDVOICE_DEBUG"))
	return v == "1" || strings.EqualFold(v, "true")
}

// -----------------------------------------------------------------------------
// HTML form model
// -----------------------------------------------------------------------------

// control is one input, select or textarea on a page.
type control struct {
	Kind    string // "hidden", "text", "radio", "checkbox", "select", "textarea", "submit"
	Name    string
	Value   string
	Label   string   // visible or accessible label text, used to choose an answer
	Options []option // radio alternatives or select options
}

// option is one choice within a radio group or select.
type option struct {
	Value string
	Label string
}

// pageForm is the single form on a survey page.
type pageForm struct {
	Action   *url.URL
	Controls []control
}

// control returns the named control, if present.
func (f pageForm) control(name string) (control, bool) {
	for _, c := range f.Controls {
		if c.Name == name {
			return c, true
		}
	}
	return control{}, false
}

// -----------------------------------------------------------------------------
// Client
// -----------------------------------------------------------------------------

type liveSurveyClient struct {
	http *http.Client
	// transcript records every step for the caller, since a failed walk is only
	// debuggable if you can see where it stopped.
	transcript []string
}

func newLiveSurveyClient() (*liveSurveyClient, error) {
	// The entry URL redirects to ?AspxAutoDetectCookieSupport=1 and expects the
	// session cookie to survive, so a jar is mandatory, not an optimization.
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("creating cookie jar: %w", err)
	}
	return &liveSurveyClient{
		http: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
	}, nil
}

func (c *liveSurveyClient) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	c.transcript = append(c.transcript, line)
	if debugEnabled() {
		fmt.Fprintln(os.Stderr, "[mcdvoice] "+line)
	}
}

// get fetches a page and parses its form.
func (c *liveSurveyClient) get(ctx context.Context, rawURL string) (pageForm, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return pageForm{}, "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	return c.do(req)
}

// post submits values to a form action.
func (c *liveSurveyClient) post(ctx context.Context, action *url.URL, values url.Values, referer string) (pageForm, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, action.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return pageForm{}, "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	return c.do(req)
}

func (c *liveSurveyClient) do(req *http.Request) (pageForm, string, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return pageForm{}, "", fmt.Errorf("%s %s: %w", req.Method, req.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded read: a survey page is tens of kilobytes, and an unbounded
	// io.ReadAll on a hostile or broken response is a memory hazard.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return pageForm{}, "", fmt.Errorf("reading %s: %w", req.URL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return pageForm{}, string(body), fmt.Errorf("%s %s returned HTTP %d", req.Method, req.URL, resp.StatusCode)
	}

	form, err := parseForm(string(body), resp.Request.URL)
	if err != nil {
		// A page with no form is not automatically an error: the final
		// confirmation page has none. The caller decides.
		return pageForm{}, string(body), nil
	}
	return form, string(body), nil
}

// -----------------------------------------------------------------------------
// Parsing
// -----------------------------------------------------------------------------

// parseForm extracts the first form that has a submit control, which skips
// decorative search boxes and language pickers.
func parseForm(body string, pageURL *url.URL) (pageForm, error) {
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return pageForm{}, fmt.Errorf("parsing HTML: %w", err)
	}

	var forms []pageForm
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			if f, ok := buildForm(n, pageURL); ok {
				forms = append(forms, f)
			}
			return // forms do not nest
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	for _, f := range forms {
		for _, c := range f.Controls {
			if c.Kind == "submit" {
				return f, nil
			}
		}
	}
	if len(forms) > 0 {
		return forms[0], nil
	}
	return pageForm{}, fmt.Errorf("no form found")
}

func buildForm(formNode *html.Node, pageURL *url.URL) (pageForm, bool) {
	action := attr(formNode, "action")
	actionURL := pageURL
	if action != "" {
		ref, err := url.Parse(action)
		if err != nil {
			return pageForm{}, false
		}
		actionURL = pageURL.ResolveReference(ref)
	}

	f := pageForm{Action: actionURL}
	// radios accumulate by name, since each <input type=radio> is one option of
	// a group rather than a control of its own.
	radioIdx := map[string]int{}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "input":
				name := attr(n, "name")
				kind := strings.ToLower(attr(n, "type"))
				if kind == "" {
					kind = "text"
				}
				if name == "" && kind != "submit" {
					break
				}
				switch kind {
				case "radio":
					opt := option{Value: attr(n, "value"), Label: labelFor(n)}
					if i, ok := radioIdx[name]; ok {
						f.Controls[i].Options = append(f.Controls[i].Options, opt)
					} else {
						radioIdx[name] = len(f.Controls)
						f.Controls = append(f.Controls, control{
							Kind:    "radio",
							Name:    name,
							Label:   questionFor(n),
							Options: []option{opt},
						})
					}
				case "hidden", "text", "checkbox", "submit", "number", "tel", "email":
					k := kind
					if k == "number" || k == "tel" || k == "email" {
						k = "text"
					}
					f.Controls = append(f.Controls, control{
						Kind:  k,
						Name:  name,
						Value: attr(n, "value"),
						Label: firstNonEmpty(attr(n, "aria-label"), labelFor(n)),
					})
				}
			case "textarea":
				if name := attr(n, "name"); name != "" {
					f.Controls = append(f.Controls, control{
						Kind:  "textarea",
						Name:  name,
						Label: firstNonEmpty(attr(n, "aria-label"), questionFor(n)),
					})
				}
			case "select":
				if name := attr(n, "name"); name != "" {
					c := control{Kind: "select", Name: name, Label: questionFor(n)}
					for o := range descendants(n) {
						if o.Type == html.ElementNode && o.Data == "option" {
							c.Options = append(c.Options, option{
								Value: attr(o, "value"),
								Label: strings.TrimSpace(textOf(o)),
							})
						}
					}
					f.Controls = append(f.Controls, c)
				}
				return // options already collected
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(formNode)
	return f, true
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// descendants yields every node beneath n.
func descendants(n *html.Node) func(func(*html.Node) bool) {
	return func(yield func(*html.Node) bool) {
		var walk func(*html.Node) bool
		walk = func(node *html.Node) bool {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if !yield(child) || !walk(child) {
					return false
				}
			}
			return true
		}
		walk(n)
	}
}

func textOf(n *html.Node) string {
	var b strings.Builder
	if n.Type == html.TextNode {
		b.WriteString(n.Data)
	}
	for c := range descendants(n) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// labelFor finds the text describing a single input: its aria-label, the
// <label> wrapping it, or the text immediately following it.
func labelFor(n *html.Node) string {
	if v := attr(n, "aria-label"); v != "" {
		return v
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && p.Data == "label" {
			return textOf(p)
		}
	}
	if next := n.NextSibling; next != nil {
		if t := strings.TrimSpace(textOf(next)); t != "" {
			return t
		}
	}
	return ""
}

// questionFor walks up to find the question text a control belongs to. SMG
// renders questions as table rows or divs above the answer controls, so the
// nearest ancestor carrying real text is the best available signal.
func questionFor(n *html.Node) string {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type != html.ElementNode {
			continue
		}
		switch p.Data {
		case "tr", "fieldset", "table", "div", "li", "td":
			if t := textOf(p); len(t) > 8 && len(t) < 400 {
				return t
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// -----------------------------------------------------------------------------
// Answering
// -----------------------------------------------------------------------------

// satisfiedKeywords rank answer labels from most to least positive. A page is
// answered by picking the highest-ranked option present.
var satisfiedKeywords = []string{
	"highly satisfied",
	"very satisfied",
	"extremely satisfied",
	"completely satisfied",
	"definitely will",
	"definitely would",
	"strongly agree",
	"excellent",
	"satisfied",
	"agree",
	"yes",
}

var negativeKeywords = []string{
	"dissatisfied",
	"disagree",
	"not at all",
	"never",
	"poor",
	"no",
}

// chooseOption picks the answer for one radio group or select.
//
// rating is the user's standing satisfaction, 1-5. At 5 the most positive
// option is chosen; lower ratings index proportionally down an ordered scale.
func chooseOption(c control, rating int) (string, string) {
	usable := make([]option, 0, len(c.Options))
	for _, o := range c.Options {
		// Placeholder options carry an empty value and must not be submitted.
		if strings.TrimSpace(o.Value) == "" {
			continue
		}
		usable = append(usable, o)
	}
	if len(usable) == 0 {
		return "", ""
	}

	// Prefer an explicit keyword match, which is more reliable than position:
	// SMG renders some scales most-positive-first and others last.
	if rating >= 4 {
		for _, kw := range satisfiedKeywords {
			for _, o := range usable {
				l := strings.ToLower(o.Label)
				if strings.Contains(l, kw) && !containsAny(l, negativeKeywords) {
					return o.Value, o.Label
				}
			}
		}
	}

	// No keyword matched, so fall back to position on the assumption the options
	// are an ordered scale. Which end is "best" is unknowable here, so the
	// first option is taken for a high rating; scales that run the other way
	// are exactly what MCDVOICE_DEBUG is for.
	idx := 0
	if rating <= 3 && len(usable) > 1 {
		idx = ((5 - rating) * (len(usable) - 1)) / 4
		if idx >= len(usable) {
			idx = len(usable) - 1
		}
	}
	return usable[idx].Value, usable[idx].Label
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// answerPage builds the POST body for one survey page.
func (c *liveSurveyClient) answerPage(f pageForm, rating int, comment string) url.Values {
	values := url.Values{}
	commentUsed := false

	for _, ctl := range f.Controls {
		switch ctl.Kind {
		case "hidden":
			// Hidden state is echoed back untouched; it carries the session and
			// page position.
			values.Set(ctl.Name, ctl.Value)
		case "radio", "select":
			if v, label := chooseOption(ctl, rating); v != "" {
				values.Set(ctl.Name, v)
				c.logf("answered %q -> %q (%.60s)", ctl.Name, label, ctl.Label)
			}
		case "textarea":
			if comment != "" && !commentUsed {
				values.Set(ctl.Name, comment)
				commentUsed = true
				c.logf("wrote comment into %q", ctl.Name)
			} else {
				values.Set(ctl.Name, "")
			}
		case "text":
			// Free-text questions are left blank. Inventing an answer is both
			// dishonest and a common way to trip validation.
			values.Set(ctl.Name, ctl.Value)
		}
	}

	// Exactly one submit button, preferring the one that advances.
	var submitName, submitValue string
	for _, ctl := range f.Controls {
		if ctl.Kind != "submit" || ctl.Name == "" {
			continue
		}
		n := strings.ToLower(ctl.Name)
		if strings.Contains(n, "next") || strings.Contains(n, "finish") || strings.Contains(n, "submit") {
			submitName, submitValue = ctl.Name, ctl.Value
			break
		}
		if submitName == "" {
			submitName, submitValue = ctl.Name, ctl.Value
		}
	}
	if submitName != "" {
		values.Set(submitName, submitValue)
	}
	return values
}

// -----------------------------------------------------------------------------
// Validation code extraction
// -----------------------------------------------------------------------------

// validationCodeRe matches the code on the confirmation page. The label wording
// varies, so the number is anchored to a nearby "validation code" phrase rather
// than matched on its own — an unanchored \d{5} would match a store number.
var validationCodeRe = regexp.MustCompile(`(?is)validation\s*code[^0-9]{0,80}(\d{4,8})`)

// altCodeRe is the fallback: a standalone 5-digit run inside an element that
// mentions a code.
var altCodeRe = regexp.MustCompile(`(?is)\bcode\b[^0-9]{0,120}(\d{5})\b`)

func extractValidationCode(body string) string {
	text := stripTags(body)
	if m := validationCodeRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	if m := altCodeRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// Go's regexp is RE2, which has no backreferences, so script and style cannot
// be matched with a single \1 pattern — they get one expression each. They are
// removed before tags because their CONTENTS would otherwise survive as text
// and feed JavaScript literals to the code extractor.
var (
	scriptRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	styleRe  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	tagRe    = regexp.MustCompile(`<[^>]*>`)
)

func stripTags(body string) string {
	body = scriptRe.ReplaceAllString(body, " ")
	body = styleRe.ReplaceAllString(body, " ")
	return strings.Join(strings.Fields(tagRe.ReplaceAllString(body, " ")), " ")
}

// -----------------------------------------------------------------------------
// The walk
// -----------------------------------------------------------------------------

// submitLiveSurvey completes a real McDVoice survey and returns the validation
// code plus a transcript of every step.
func submitLiveSurvey(ctx context.Context, chunks []string, visitType string, rating int, comment string) (code string, transcript []string, err error) {
	client, err := newLiveSurveyClient()
	if err != nil {
		return "", nil, err
	}
	defer func() { transcript = client.transcript }()

	// Step 1: the entry page, whose structure is known from observation.
	entry, body, err := client.get(ctx, mcdVoiceEntryURL)
	if err != nil {
		return "", client.transcript, fmt.Errorf("fetching entry page: %w", err)
	}
	if entry.Action == nil {
		return "", client.transcript, fmt.Errorf("entry page had no form; the site layout changed")
	}
	if _, ok := entry.control("CN1"); !ok {
		return "", client.transcript, fmt.Errorf("entry page has no CN1 field; the site layout changed")
	}
	client.logf("entry form at %s", entry.Action)

	values := url.Values{}
	for _, ctl := range entry.Controls {
		if ctl.Kind == "hidden" {
			values.Set(ctl.Name, ctl.Value)
		}
	}
	for i, chunk := range chunks {
		values.Set(fmt.Sprintf("CN%d", i+1), chunk)
	}
	// Observed on the live page: JavaScriptEnabled defaults to 0 and the engine
	// serves a no-JS fallback for that, which is the simpler flow to drive.
	values.Set("JavaScriptEnabled", "0")
	values.Set("FIP", "True")
	values.Set("AllowCapture", "")
	values.Set("NextButton", "Start")

	form, body, err := client.post(ctx, entry.Action, values, mcdVoiceEntryURL)
	if err != nil {
		return "", client.transcript, fmt.Errorf("submitting receipt code: %w", err)
	}
	client.logf("submitted receipt code %s", strings.Join(chunks, "-"))

	// Steps 2..N: answer whatever each page serves.
	for step := 2; step <= maxSurveySteps; step++ {
		if code := extractValidationCode(body); code != "" {
			client.logf("validation code found at step %d", step)
			return code, client.transcript, nil
		}
		if form.Action == nil || len(form.Controls) == 0 {
			snippet := stripTags(body)
			if len(snippet) > 400 {
				snippet = snippet[:400]
			}
			return "", client.transcript, fmt.Errorf("survey stopped at step %d with no form and no validation code; page said: %s", step, snippet)
		}

		answers := client.answerPage(form, rating, comment)
		if comment != "" {
			// Only offer the comment once.
			comment = ""
		}
		client.logf("step %d: posting %d fields to %s", step, len(answers), form.Action)

		form, body, err = client.post(ctx, form.Action, answers, form.Action.String())
		if err != nil {
			return "", client.transcript, fmt.Errorf("step %d: %w", step, err)
		}
	}
	return "", client.transcript, fmt.Errorf("survey did not finish within %d steps", maxSurveySteps)
}

// standingRating returns the user's default satisfaction rating from
// MCDVOICE_RATING, defaulting to 5.
func standingRating() int {
	if v := strings.TrimSpace(os.Getenv("MCDVOICE_RATING")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 5 {
			return n
		}
	}
	return 5
}
