package review

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClassifyRequestUsesStrictScoreSchema(t *testing.T) {
	body, err := ClassifyRequestBody("acme/model", "hi")
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Messages []struct {
			Role, Content string
		} `json:"messages"`
		ResponseFormat struct {
			JSONSchema struct {
				Name   string `json:"name"`
				Strict bool   `json:"strict"`
				Schema struct {
					Properties struct {
						Score struct {
							Type string `json:"type"`
							Enum []int  `json:"enum"`
						} `json:"score"`
					} `json:"properties"`
				} `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
		Provider struct {
			RequireParameters bool `json:"require_parameters"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	schema := request.ResponseFormat.JSONSchema
	if schema.Name != "classification" || !schema.Strict || !request.Provider.RequireParameters {
		t.Errorf("request %s", body)
	}
	if score := schema.Schema.Properties.Score; score.Type != "integer" || len(score.Enum) != 10 || score.Enum[0] != 1 || score.Enum[9] != 10 {
		t.Errorf("score schema %+v", score)
	}
	if len(request.Messages) != 2 || request.Messages[0].Content != ClassifySystem || !strings.Contains(ClassifySystem, "1 to 10") {
		t.Errorf("messages %+v", request.Messages)
	}
}

func TestClassificationIsParsedFromCompletion(t *testing.T) {
	c, err := ParseClassification(completion(t, `{"score":3,"reason":"Only tests change."}`))
	if err != nil || c != (Classification{Score: 3, Reason: "Only tests change."}) {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestScoreOutsideTheRangeIsRejected(t *testing.T) {
	for _, score := range []string{"0", "11"} {
		if _, err := ParseClassification(completion(t, `{"score":`+score+`,"reason":"x"}`)); err == nil || !strings.Contains(err.Error(), "not from 1 to 10") {
			t.Errorf("score %s: %v", score, err)
		}
	}
}

func TestEmptyClassificationIsReported(t *testing.T) {
	if _, err := ParseClassification([]byte(`{"choices":[]}`)); err == nil || err.Error() != "The model returned no classification" {
		t.Errorf("got %v", err)
	}
}
