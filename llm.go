package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
)

func generateResponsesTurn(ctx context.Context, p OpenAIConfig, previousResponseID string, userInput string, tools []Tool, functionOutputs map[string]string, toolChoice any) (string, []ToolCall, string, error) {
	if p.MaxTokens == 0 {
		p.MaxTokens = 4096
	}

	req := map[string]interface{}{
		"model":             p.Model,
		"store":             true,
		"max_output_tokens": p.MaxTokens,
	}

	if previousResponseID != "" {
		req["previous_response_id"] = previousResponseID
		inputs := make([]map[string]interface{}, 0, len(functionOutputs))
		for callID, output := range functionOutputs {
			inputs = append(inputs, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			})
		}
		req["input"] = inputs
	} else {
		req["input"] = userInput
	}

	if len(tools) > 0 {
		toolList := make([]map[string]interface{}, 0, len(tools))
		for _, t := range tools {
			toolList = append(toolList, map[string]interface{}{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
				"strict":      true,
			})
		}
		req["tools"] = toolList
		if toolChoice != nil {
			req["tool_choice"] = toolChoice
		}
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to marshal responses request: %w", err)
	}
	apiURL := p.APIHost + "/v1/responses"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to create responses request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)

	client := &http.Client{}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to send responses request: %w", err)
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusOK {
		return "", nil, "", fmt.Errorf("responses api returned status %d: %s", httpResp.StatusCode, string(body))
	}

	assistantText, toolCalls, responseID, err := parseResponsesOutput(body)
	if err != nil {
		return "", nil, "", err
	}
	return assistantText, toolCalls, responseID, nil
}

func parseResponsesOutput(body []byte) (assistantText string, toolCalls []ToolCall, responseID string, err error) {
	var root struct {
		ID     string          `json:"id"`
		Output json.RawMessage `json:"output"`
	}
	if e := json.Unmarshal(body, &root); e != nil {
		return "", nil, "", fmt.Errorf("failed to decode responses body: %w", e)
	}
	responseID = root.ID

	var items []map[string]any
	if e := json.Unmarshal(root.Output, &items); e != nil {
		var alt struct {
			Output []map[string]any `json:"output"`
		}
		if e2 := json.Unmarshal(body, &alt); e2 == nil && len(alt.Output) > 0 {
			items = alt.Output
		} else {
			// It might be a single object, not an array
			var singleItem map[string]any
			if e3 := json.Unmarshal(root.Output, &singleItem); e3 == nil {
				items = []map[string]any{singleItem}
			} else {
				return "", nil, responseID, fmt.Errorf("unexpected responses output format: %v", e)
			}
		}
	}

	var textBuilder []string
	var calls []ToolCall
	for _, it := range items {
		t, _ := it["type"].(string)
		switch t {
		case "message":
			if content, ok := it["content"].([]any); ok {
				for _, c := range content {
					if cm, ok := c.(map[string]any); ok {
						if cm["type"] == "output_text" {
							if txt, _ := cm["text"].(string); txt != "" {
								textBuilder = append(textBuilder, txt)
							}
						}
					}
				}
			}
			if mtc, ok := it["tool_calls"].([]any); ok {
				for _, raw := range mtc {
					if m, ok := raw.(map[string]any); ok {
						id, _ := m["id"].(string)
						if fn, ok := m["function"].(map[string]any); ok {
							name, _ := fn["name"].(string)
							args, _ := fn["arguments"].(string)
							if id != "" && name != "" {
								calls = append(calls, ToolCall{ID: id, Name: name, Arguments: args})
							}
						}
					}
				}
			}
		case "function_call":
			id, _ := it["call_id"].(string)
			name, _ := it["name"].(string)
			var argsStr string
			if s, ok := it["arguments"].(string); ok {
				argsStr = s
			} else if obj, ok := it["arguments"].(map[string]any); ok {
				if b, e := json.Marshal(obj); e == nil {
					argsStr = string(b)
				}
			}
			if id != "" && name != "" {
				calls = append(calls, ToolCall{ID: id, Name: name, Arguments: argsStr})
			}
		}
	}

	return strings.TrimSpace(strings.Join(textBuilder, "\n")), calls, responseID, nil
}

func structToJSONSchema(s any) (map[string]any, error) {
	val := reflect.ValueOf(s)
	typ := val.Type()

	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}

	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected a struct, got %s", typ.Kind())
	}

	properties := make(map[string]any)
	var required []string

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue // Skip fields without json tag or marked to be ignored
		}

		parts := strings.Split(jsonTag, ",")
		fieldName := parts[0]

		prop := make(map[string]any)
		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		switch fieldType.Kind() {
		case reflect.String:
			prop["type"] = "string"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			prop["type"] = "integer"
		case reflect.Float32, reflect.Float64:
			prop["type"] = "number"
		case reflect.Bool:
			prop["type"] = "boolean"
		case reflect.Slice:
			prop["type"] = "array"
			elemType := fieldType.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}

			items := make(map[string]any)
			switch elemType.Kind() {
			case reflect.String:
				items["type"] = "string"
			default:
				items["type"] = "string" // Fallback for simplicity
			}
			prop["items"] = items
		default:
			continue
		}
		properties[fieldName] = prop
		required = append(required, fieldName)
	}

	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}, nil
}

// generateJSONResponse instructs the LLM to respond with a specific JSON structure.
func generateJSONResponse(ctx context.Context, p OpenAIConfig, prompt, userInput string, targetJSON any) ([]byte, error) {
	schema, err := structToJSONSchema(targetJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to generate schema from target struct: %w", err)
	}
	tool := Tool{
		Name:        "json_response",
		Description: "A tool to provide a JSON response.",
		Parameters:  schema,
	}

	toolChoice := "required" // This API expects 'required' to force a tool call.

	// We pass the tool and also explicitly ask the model to use it.
	_, toolCalls, _, err := generateResponsesTurn(ctx, p, "", userInput, []Tool{tool}, nil, toolChoice)
	if err != nil {
		return nil, fmt.Errorf("failed to generate JSON response: %w", err)
	}

	if len(toolCalls) == 0 {
		return nil, fmt.Errorf("LLM did not return the expected tool call")
	}

	// Extract the arguments from the first tool call.
	return []byte(toolCalls[0].Arguments), nil
}

// ParsePollRequestWithLLM uses an LLM to parse a natural language poll request
// into a structured format.
func ParsePollRequestWithLLM(ctx context.Context, p OpenAIConfig, pollRequest string) (ParsedPollRequest, error) {
	prompt := appConfig.PollParserPrompt
	if prompt == "" {
		return ParsedPollRequest{}, fmt.Errorf("POLL_PARSER_SYSTEM_PROMPT not configured")
	}

	var parsedRequest ParsedPollRequest
	jsonBytes, err := generateJSONResponse(ctx, p, prompt, pollRequest, &parsedRequest)
	if err != nil {
		return ParsedPollRequest{}, err
	}

	err = json.Unmarshal(jsonBytes, &parsedRequest)
	if err != nil {
		return ParsedPollRequest{}, fmt.Errorf("failed to unmarshal LLM response: %w", err)
	}

	// After parsing, remove the "@" prefix from all usernames.
	for i, username := range parsedRequest.Usernames {
		parsedRequest.Usernames[i] = strings.TrimPrefix(username, "@")
	}

	// Limit to 5 users per poll; we can change later, this is just to enable deployment
	if len(parsedRequest.Usernames) > 5 {
		parsedRequest.Usernames = parsedRequest.Usernames[:5]
	}
	return parsedRequest, nil
}
