package agents

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestParseReceiptCode(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantValid  bool
		wantDigits string
		wantChunks []string
		reasonHas  string
	}{
		{
			name:       "dashed 26-digit code",
			raw:        "12345-67890-12345-67890-12345-6",
			wantValid:  true,
			wantDigits: "12345678901234567890123456",
			wantChunks: []string{"12345", "67890", "12345", "67890", "12345", "6"},
		},
		{
			name:       "spaces and stray letters are stripped",
			raw:        "  CN: 1234 5678 9012 3456 7890 1234 56  ",
			wantValid:  true,
			wantDigits: "12345678901234567890123456",
			wantChunks: []string{"12345", "67890", "12345", "67890", "12345", "6"},
		},
		{
			name:      "too short",
			raw:       "12345-67890",
			wantValid: false,
			reasonHas: "Found 10 digits",
		},
		{
			name:      "too long",
			raw:       strings.Repeat("1", 27),
			wantValid: false,
			reasonHas: "Found 27 digits",
		},
		{
			name:      "no digits at all",
			raw:       "where is my code",
			wantValid: false,
			reasonHas: "No digits found",
		},
		{
			name:      "empty",
			raw:       "",
			wantValid: false,
			reasonHas: "No digits found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseReceiptCode(nil, ParseReceiptCodeArgs{RawCode: tc.raw})
			if err != nil {
				t.Fatalf("parseReceiptCode(%q) returned error %v; invalid input must come back as Valid:false, not an error", tc.raw, err)
			}
			if got.Valid != tc.wantValid {
				t.Fatalf("Valid = %v, want %v (reason: %q)", got.Valid, tc.wantValid, got.Reason)
			}
			if !tc.wantValid {
				if !strings.Contains(got.Reason, tc.reasonHas) {
					t.Errorf("Reason = %q, want it to contain %q", got.Reason, tc.reasonHas)
				}
				if len(got.Chunks) != 0 {
					t.Errorf("Chunks = %v, want none for an invalid code", got.Chunks)
				}
				return
			}
			if got.NormalizedDigits != tc.wantDigits {
				t.Errorf("NormalizedDigits = %q, want %q", got.NormalizedDigits, tc.wantDigits)
			}
			if got.DigitCount != mcdVoiceCodeDigits {
				t.Errorf("DigitCount = %d, want %d", got.DigitCount, mcdVoiceCodeDigits)
			}
			if strings.Join(got.Chunks, ",") != strings.Join(tc.wantChunks, ",") {
				t.Errorf("Chunks = %v, want %v", got.Chunks, tc.wantChunks)
			}
			if got.Reason != "" {
				t.Errorf("Reason = %q, want empty for a valid code", got.Reason)
			}
		})
	}
}

// TestChunkSizesSumToCodeLength guards the two constants against drifting apart.
func TestChunkSizesSumToCodeLength(t *testing.T) {
	sum := 0
	for _, n := range mcdVoiceChunkSizes {
		sum += n
	}
	if sum != mcdVoiceCodeDigits {
		t.Fatalf("chunk sizes sum to %d but mcdVoiceCodeDigits is %d", sum, mcdVoiceCodeDigits)
	}
}

func TestSubmitRejectsUntrustedArgs(t *testing.T) {
	valid := []string{"12345", "67890", "12345", "67890", "12345", "6"}

	tests := []struct {
		name    string
		args    SubmitSurveyArgs
		wantErr string
	}{
		{
			name:    "wrong chunk count",
			args:    SubmitSurveyArgs{Chunks: valid[:3], OverallSatisfaction: 5},
			wantErr: "expected 6 chunks",
		},
		{
			name:    "chunk wrong width",
			args:    SubmitSurveyArgs{Chunks: []string{"1234", "67890", "12345", "67890", "12345", "6"}, OverallSatisfaction: 5},
			wantErr: "CN1",
		},
		{
			name:    "non-digit chunk",
			args:    SubmitSurveyArgs{Chunks: []string{"1234X", "67890", "12345", "67890", "12345", "6"}, OverallSatisfaction: 5},
			wantErr: "non-digit",
		},
		{
			name:    "rating out of range",
			args:    SubmitSurveyArgs{Chunks: valid, OverallSatisfaction: 9},
			wantErr: "overall_satisfaction must be 1-5",
		},
		{
			name:    "rating zero",
			args:    SubmitSurveyArgs{Chunks: valid, OverallSatisfaction: 0},
			wantErr: "overall_satisfaction must be 1-5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := submitMcDVoiceSurvey(nil, tc.args)
			if err == nil {
				t.Fatalf("submitMcDVoiceSurvey accepted bad args %+v; a tool must not trust model-generated input", tc.args)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestSimulatedSubmissionIsLabelled is the important one: a simulated code must
// never be presented as redeemable.
func TestSimulatedSubmissionIsLabelled(t *testing.T) {
	t.Setenv("MCDVOICE_LIVE", "")
	args := SubmitSurveyArgs{
		Chunks:              []string{"12345", "67890", "12345", "67890", "12345", "6"},
		VisitType:           "Drive-Thru",
		OverallSatisfaction: 5,
	}
	got, err := submitMcDVoiceSurvey(nil, args)
	if err != nil {
		t.Fatalf("submitMcDVoiceSurvey() failed: %v", err)
	}
	if got.Mode != "simulation" {
		t.Errorf("Mode = %q, want simulation when MCDVOICE_LIVE is unset", got.Mode)
	}
	if !strings.Contains(got.Notice, "SIMULATED") {
		t.Errorf("Notice = %q, want it to say the code is SIMULATED", got.Notice)
	}
	if got.ValidationCode == "" || len(got.ValidationCode) != 5 {
		t.Errorf("ValidationCode = %q, want 5 digits", got.ValidationCode)
	}

	// Deterministic: same receipt, same code, so agent runs are reproducible.
	again, err := submitMcDVoiceSurvey(nil, args)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if again.ValidationCode != got.ValidationCode {
		t.Errorf("code changed between runs: %q then %q", got.ValidationCode, again.ValidationCode)
	}
}

func TestNormalizeVisitType(t *testing.T) {
	tests := map[string]string{
		"":              "Drive-Thru",
		"drive thru":    "Drive-Thru",
		"Drive-Thru":    "Drive-Thru",
		"dine in":       "Dine-In",
		"DINE-IN":       "Dine-In",
		"ate in":        "Dine-In",
		"carry out":     "Carry-Out",
		"Takeout":       "Carry-Out",
		"to go":         "Carry-Out",
		"something odd": "Drive-Thru",
	}
	for in, want := range tests {
		if got := normalizeVisitType(in); got != want {
			t.Errorf("normalizeVisitType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStandingRating(t *testing.T) {
	tests := map[string]int{"": 5, "5": 5, "1": 1, "3": 3, "0": 5, "6": 5, "abc": 5}
	for in, want := range tests {
		t.Run("env="+in, func(t *testing.T) {
			if in == "" {
				t.Setenv("MCDVOICE_RATING", "")
				_ = os.Unsetenv("MCDVOICE_RATING")
			} else {
				t.Setenv("MCDVOICE_RATING", in)
			}
			if got := standingRating(); got != want {
				t.Errorf("standingRating() with MCDVOICE_RATING=%q = %d, want %d", in, got, want)
			}
		})
	}
}

func TestLiveEnabled(t *testing.T) {
	tests := map[string]bool{"": false, "0": false, "no": false, "1": true, "true": true, "TRUE": true}
	for in, want := range tests {
		t.Run("env="+in, func(t *testing.T) {
			t.Setenv("MCDVOICE_LIVE", in)
			if got := liveEnabled(); got != want {
				t.Errorf("liveEnabled() with MCDVOICE_LIVE=%q = %v, want %v", in, got, want)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Form parsing, against the real page captured from mcdvoice.com.
// -----------------------------------------------------------------------------

func TestParseRealEntryForm(t *testing.T) {
	body, err := os.ReadFile("testdata/mcdvoice_entry.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	pageURL, _ := url.Parse("https://www.mcdvoice.com/?AspxAutoDetectCookieSupport=1")

	form, err := parseForm(string(body), pageURL)
	if err != nil {
		t.Fatalf("parseForm() on the real entry page failed: %v", err)
	}

	// The action is relative ("Survey.aspx?c=...") and must resolve absolutely,
	// or the POST goes nowhere.
	if form.Action == nil {
		t.Fatal("Action is nil")
	}
	if form.Action.Host != "www.mcdvoice.com" {
		t.Errorf("Action host = %q, want www.mcdvoice.com (relative action did not resolve)", form.Action.Host)
	}
	if !strings.Contains(form.Action.Path, "Survey.aspx") {
		t.Errorf("Action path = %q, want it to contain Survey.aspx", form.Action.Path)
	}

	// All six code fields must be found, or a live run cannot submit.
	for i := 1; i <= 6; i++ {
		name := "CN" + string(rune('0'+i))
		c, ok := form.control(name)
		if !ok {
			t.Errorf("control %s not found", name)
			continue
		}
		if c.Kind != "text" {
			t.Errorf("%s kind = %q, want text", name, c.Kind)
		}
	}

	// The hidden state the engine expects back.
	for _, name := range []string{"JavaScriptEnabled", "FIP", "AllowCapture"} {
		if _, ok := form.control(name); !ok {
			t.Errorf("hidden control %s not found", name)
		}
	}

	// A submit control is what lets parseForm tell this form from decorative ones.
	var hasSubmit bool
	for _, c := range form.Controls {
		if c.Kind == "submit" {
			hasSubmit = true
		}
	}
	if !hasSubmit {
		t.Error("no submit control found")
	}
}

func TestChooseOptionPrefersPositiveLabel(t *testing.T) {
	// Options deliberately ordered worst-first, so a positional guess would
	// pick the wrong one and only keyword matching gets it right.
	c := control{
		Kind: "radio",
		Name: "Q1",
		Options: []option{
			{Value: "1", Label: "Highly Dissatisfied"},
			{Value: "2", Label: "Dissatisfied"},
			{Value: "3", Label: "Neither"},
			{Value: "4", Label: "Satisfied"},
			{Value: "5", Label: "Highly Satisfied"},
		},
	}
	gotValue, gotLabel := chooseOption(c, 5)
	if gotValue != "5" {
		t.Errorf("chooseOption(rating 5) = %q (%q), want value 5 / Highly Satisfied", gotValue, gotLabel)
	}
}

func TestChooseOptionSkipsPlaceholders(t *testing.T) {
	c := control{
		Kind: "select",
		Name: "Q2",
		Options: []option{
			{Value: "", Label: "-- Please select --"},
			{Value: "7", Label: "Yes"},
			{Value: "8", Label: "No"},
		},
	}
	got, _ := chooseOption(c, 5)
	if got == "" {
		t.Fatal("chooseOption returned the empty placeholder value, which would fail form validation")
	}
	if got != "7" {
		t.Errorf("chooseOption = %q, want 7 (Yes)", got)
	}
}

func TestChooseOptionNoUsableOptions(t *testing.T) {
	c := control{Kind: "radio", Name: "Q3", Options: []option{{Value: "", Label: "placeholder"}}}
	if got, _ := chooseOption(c, 5); got != "" {
		t.Errorf("chooseOption = %q, want empty when nothing is selectable", got)
	}
}

func TestExtractValidationCode(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "labelled code",
			body: `<div>Your validation code is <b>71423</b>. Write it on your receipt.</div>`,
			want: "71423",
		},
		{
			name: "code with colon and markup between",
			body: `<p>Validation Code:</p><span class="code">  48201 </span>`,
			want: "48201",
		},
		{
			name: "store number must not be mistaken for a code",
			body: `<div>Store #004707</div><div>Thanks for your feedback.</div>`,
			want: "",
		},
		{
			name: "no code present",
			body: `<html><body>Survey closed.</body></html>`,
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractValidationCode(tc.body); got != tc.want {
				t.Errorf("extractValidationCode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAnswerPageEchoesHiddenState checks that hidden fields survive a round
// trip. Dropping them loses the survey's page position and the engine restarts
// or errors.
func TestAnswerPageEchoesHiddenState(t *testing.T) {
	c := &liveSurveyClient{}
	f := pageForm{
		Controls: []control{
			{Kind: "hidden", Name: "SessionToken", Value: "abc123"},
			{Kind: "hidden", Name: "PageNum", Value: "4"},
			{Kind: "radio", Name: "Q1", Label: "Overall satisfaction", Options: []option{
				{Value: "5", Label: "Highly Satisfied"},
				{Value: "1", Label: "Highly Dissatisfied"},
			}},
			{Kind: "textarea", Name: "Comments"},
			{Kind: "submit", Name: "NextButton", Value: "Next"},
		},
	}

	got := c.answerPage(f, 5, "Great service as always")

	if got.Get("SessionToken") != "abc123" {
		t.Errorf("SessionToken = %q, want abc123 echoed back", got.Get("SessionToken"))
	}
	if got.Get("PageNum") != "4" {
		t.Errorf("PageNum = %q, want 4 echoed back", got.Get("PageNum"))
	}
	if got.Get("Q1") != "5" {
		t.Errorf("Q1 = %q, want 5 (Highly Satisfied)", got.Get("Q1"))
	}
	if got.Get("Comments") != "Great service as always" {
		t.Errorf("Comments = %q, want the supplied comment", got.Get("Comments"))
	}
	if got.Get("NextButton") != "Next" {
		t.Errorf("NextButton = %q, want Next so the page advances", got.Get("NextButton"))
	}
}

func TestAnswerPagePicksAdvancingSubmit(t *testing.T) {
	c := &liveSurveyClient{}
	f := pageForm{
		Controls: []control{
			{Kind: "submit", Name: "BackButton", Value: "Back"},
			{Kind: "submit", Name: "NextButton", Value: "Next"},
		},
	}
	got := c.answerPage(f, 5, "")
	if got.Get("NextButton") != "Next" {
		t.Errorf("NextButton not set; values = %v. Posting Back would walk the survey backwards.", got)
	}
	if got.Has("BackButton") {
		t.Errorf("BackButton was also posted: %v. Two submits in one body is ambiguous.", got)
	}
}
