package schema_test

import (
	"encoding/json"
	"testing"

	"github.com/JollyGrin/grove/internal/schema"
)

func TestEnvelope(t *testing.T) {
	raw, err := json.Marshal(schema.Envelope("tasks", []string{"a"}))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		SchemaVersion int      `json:"schema_version"`
		Tasks         []string `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.SchemaVersion != schema.Version {
		t.Errorf("schema_version = %d, want %d", out.SchemaVersion, schema.Version)
	}
	if len(out.Tasks) != 1 || out.Tasks[0] != "a" {
		t.Errorf("payload lost in envelope: %+v", out)
	}
}
