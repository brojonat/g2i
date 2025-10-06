package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// ParsePollRequestWithLLM uses an LLM to parse a natural language poll request
// into a structured format.
func ParsePollRequestWithLLM(ctx context.Context, pollRequest string) (ParsedPollRequest, error) {
	// TODO: Replace this with a real LLM call
	mockResponse := `{"question": "Who is the best meme creator?", "usernames": ["user1", "user2", "user3"]}`
	var parsedRequest ParsedPollRequest
	err := json.Unmarshal([]byte(mockResponse), &parsedRequest)
	if err != nil {
		return ParsedPollRequest{}, fmt.Errorf("failed to unmarshal mock response: %w", err)
	}

	return parsedRequest, nil
}
