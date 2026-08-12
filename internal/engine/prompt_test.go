package engine

import "testing"

func TestParsePromptLine_Prompt(t *testing.T) {
	msg, ok := ParsePromptLine("prompt field=APP_URL kind=url required=true attempt=1")
	if !ok {
		t.Fatal("expected a prompt line to parse")
	}
	p, ok := msg.(Prompt)
	if !ok {
		t.Fatalf("expected Prompt, got %T", msg)
	}
	if p.Field != "APP_URL" || p.Kind != "url" || p.Attempt != 1 || !p.Required {
		t.Fatalf("unexpected prompt: %+v", p)
	}
}

func TestParsePromptLine_RejectAcceptAbort(t *testing.T) {
	msg, ok := ParsePromptLine("prompt-reject field=APP_URL reason=not-https")
	if !ok {
		t.Fatal("expected reject to parse")
	}
	if r := msg.(PromptReject); r.Field != "APP_URL" || r.Reason != "not-https" {
		t.Fatalf("unexpected reject: %+v", r)
	}

	msg, ok = ParsePromptLine("prompt-accept field=OIDC_CLIENT_ID")
	if !ok {
		t.Fatal("expected accept to parse")
	}
	if a := msg.(PromptAccept); a.Field != "OIDC_CLIENT_ID" {
		t.Fatalf("unexpected accept: %+v", a)
	}

	msg, ok = ParsePromptLine("prompt-abort field=OIDC_CLIENT_SECRET")
	if !ok {
		t.Fatal("expected abort to parse")
	}
	if a := msg.(PromptAbort); a.Field != "OIDC_CLIENT_SECRET" {
		t.Fatalf("unexpected abort: %+v", a)
	}
}

func TestParsePromptLine_ToleratesUnknownTrailingFields(t *testing.T) {
	msg, ok := ParsePromptLine("prompt field=APP_URL kind=url required=true attempt=2 shade=gold")
	if !ok {
		t.Fatal("expected a valid prompt — unknown trailing key=value fields must be tolerated")
	}
	if p := msg.(Prompt); p.Attempt != 2 {
		t.Fatalf("unexpected prompt: %+v", p)
	}
}

func TestParsePromptLine_UnknownEnumValuesCarriedVerbatim(t *testing.T) {
	msg, ok := ParsePromptLine("prompt field=NEW_FIELD kind=phone required=true attempt=1")
	if !ok {
		t.Fatal("expected a valid prompt — unknown field/kind values are renderable, not rejected")
	}
	if p := msg.(Prompt); p.Field != "NEW_FIELD" || p.Kind != "phone" {
		t.Fatalf("unexpected prompt: %+v", p)
	}
}

func TestParsePromptLine_RejectsProse(t *testing.T) {
	for _, line := range []string{
		"",
		"Orbit guided configuration saved APP_URL.",
		"prompt the engine would like a word",
		"prompt",
		"prompt-reject field=APP_URL",      // reject without a reason is not a protocol line
		"prompting field=APP_URL kind=url", // near-miss lead word
		"phase=configuration component=configuration state=failed reason=configuration-failure action=configure elapsed=1s",
	} {
		if _, ok := ParsePromptLine(line); ok {
			t.Errorf("expected %q to be rejected", line)
		}
	}
}

func TestParsePromptLine_MalformedAttemptDefaultsToOne(t *testing.T) {
	msg, ok := ParsePromptLine("prompt field=APP_URL kind=url required=true attempt=soon")
	if !ok {
		t.Fatal("expected the line to parse")
	}
	if p := msg.(Prompt); p.Attempt != 1 {
		t.Fatalf("expected malformed attempt to default to 1, got %d", p.Attempt)
	}
}
