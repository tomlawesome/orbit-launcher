package ui

import (
	"strings"
	"testing"
)

// The Ollama paragraph and the model download (#281; owner, ai/orbit#1202
// 7a and 8a, placement 23a and wording 24a, 2026-10-21).
const noticeOllama = "Orbit can use Ollama, a program that runs language models on this machine, to read your uploads. It has no route to the internet, so nothing leaves this machine and nothing is used to train anything. The model is downloaded once at install, only if you choose the full local stack."

const noticeModelDownload = "installing and upgrading downloads files from GitHub and Docker Hub, choosing the full local stack downloads a language model from Ollama's library once, the launcher checks GitHub for a newer version when it starts"

func TestNotice_OllamaParagraphFollowsWhatOrbitReachesOnline(t *testing.T) {
	online := -1
	for i, p := range noticeParagraphs {
		if strings.HasPrefix(p, "Orbit sends no data or telemetry") {
			online = i
		}
	}
	if online < 0 {
		t.Fatal("the paragraph on what Orbit reaches online is missing")
	}
	if online+1 >= len(noticeParagraphs) || noticeParagraphs[online+1] != noticeOllama {
		t.Errorf("the paragraph after the online one should be the approved Ollama copy, verbatim; got %q", noticeParagraphs[min(online+1, len(noticeParagraphs)-1)])
	}
	if !strings.Contains(noticeParagraphs[online], noticeModelDownload) {
		t.Errorf("the online paragraph should name the model download:\n%s", noticeParagraphs[online])
	}
}

func TestNotice_OllamaParagraphIsDrawn(t *testing.T) {
	var all []string
	for _, r := range buildNoticeDoc(noticeMeasure) {
		all = append(all, r.plain())
	}
	flat := strings.Join(strings.Fields(strings.Join(all, " ")), " ")
	for _, want := range []string{noticeOllama, noticeModelDownload} {
		if !strings.Contains(flat, want) {
			t.Errorf("the drawn notice is missing %q", want)
		}
	}
}
