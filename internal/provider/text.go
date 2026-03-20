package provider

import "strings"

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type geminiCandidate struct {
	Content struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"content"`
}

func joinAnthropicText(blocks []anthropicContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		parts = append(parts, block.Text)
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func joinGeminiText(candidates []geminiCandidate) string {
	parts := make([]string, 0)
	for _, candidate := range candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			parts = append(parts, part.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}
