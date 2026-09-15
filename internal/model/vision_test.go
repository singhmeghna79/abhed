package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func imageMsg() Message {
	return Message{Role: RoleUser, Content: "what is this?", Blocks: []ContentBlock{
		{Kind: BlockText, Text: "what is this?"},
		{Kind: BlockImage, MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
	}}
}

// A text-only message must serialise byte-identically to what it did before
// blocks existed. Fourteen of the twenty providers ride the OpenAI adapter, and
// the prefix cache depends on an unchanged prefix — a body that gained an array
// wrapper would cold-start every one of them.
func TestTextOnlyContentStaysAPlainString(t *testing.T) {
	got := openAIContent(Message{Role: RoleUser, Content: "hello"})
	if s, ok := got.(string); !ok || s != "hello" {
		t.Fatalf("text-only content became %T (%v), want a plain string", got, got)
	}
}

// The OpenAI array form: text parts and a data: URI for the image. Not an http
// URL — that would have the provider fetch the bytes, taking them out of the
// network on a deployment whose premise is that they stay in.
func TestOpenAIImageIsInlineDataURI(t *testing.T) {
	raw, err := json.Marshal(openAIContent(imageMsg()))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"type":"image_url"`) {
		t.Errorf("no image_url part: %s", s)
	}
	if !strings.Contains(s, `data:image/png;base64,`) {
		t.Errorf("image is not an inline data URI: %s", s)
	}
	if strings.Contains(s, `"url":"http`) {
		t.Error("image was sent as a fetchable URL — bytes would leave the network")
	}
}

// Anthropic takes a source block with an explicit media type.
func TestAnthropicImageIsBase64Source(t *testing.T) {
	raw, err := json.Marshal(anthropicBlocks(imageMsg()))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"type":"image"`) || !strings.Contains(s, `"media_type":"image/png"`) {
		t.Errorf("not an anthropic image block: %s", s)
	}
	if !strings.Contains(s, `"type":"base64"`) {
		t.Errorf("image source is not base64: %s", s)
	}
}

// Gemini uses inlineData with a mimeType.
func TestGeminiImageIsInlineData(t *testing.T) {
	raw, err := json.Marshal(geminiParts(imageMsg()))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); !strings.Contains(s, `"inlineData"`) ||
		!strings.Contains(s, `"mimeType":"image/png"`) {
		t.Errorf("not a gemini inline image: %s", s)
	}
}

// The sharpest bug this change could have introduced. A 2 MB PNG is ~2.7M
// base64 characters; counting those as text reports ~750k tokens, which would
// trigger compaction every turn and eventually refuse a request that truly
// costs about 1,500.
func TestImageIsNotCountedByByteLength(t *testing.T) {
	big := make([]byte, 2<<20) // 2 MiB
	req := Request{Messages: []Message{{
		Role: RoleUser, Blocks: []ContentBlock{
			{Kind: BlockText, Text: "describe this"},
			{Kind: BlockImage, MediaType: "image/png", Data: big},
		},
	}}}

	got := estimateTokens(req)
	if got > 10_000 {
		t.Fatalf("a 2 MiB image estimated at %d tokens — it is being counted "+
			"by byte length, which would trigger a compaction storm", got)
	}
	if got < imageTokenEstimate {
		t.Errorf("image cost %d is below the flat estimate %d — images are "+
			"not being counted at all", got, imageTokenEstimate)
	}
}

// An image sent to a text-only model must fail loudly. Silently dropping it
// produces "the model did not mention the screenshot", which is far harder to
// diagnose than a refusal that names the cause.
func TestVisionIsRefusedByTextOnlyModels(t *testing.T) {
	req := Request{Messages: []Message{imageMsg()}}

	if err := CheckVision(Profile{SupportsVision: false}, req); !errors.Is(err, ErrVisionUnsupported) {
		t.Errorf("a text-only model accepted an image: %v", err)
	}
	if err := CheckVision(Profile{SupportsVision: true}, req); err != nil {
		t.Errorf("a vision model refused an image: %v", err)
	}
	// A text-only conversation must pass on a text-only model.
	textOnly := Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := CheckVision(Profile{SupportsVision: false}, textOnly); err != nil {
		t.Errorf("text-only request refused: %v", err)
	}
}

// Consumers that cannot show an image still need to read something sensible
// rather than an empty turn.
func TestTextOfRendersImagesAsPlaceholders(t *testing.T) {
	got := imageMsg().TextOf()
	if !strings.Contains(got, "what is this?") {
		t.Errorf("text was dropped: %q", got)
	}
	if !strings.Contains(got, "[image: image/png]") {
		t.Errorf("image has no placeholder: %q", got)
	}
	// A plain message is unchanged.
	if s := (Message{Content: "plain"}).TextOf(); s != "plain" {
		t.Errorf("plain message altered: %q", s)
	}
}
