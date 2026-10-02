package webhook

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestListEventCatalogReturnsDocumentedExamples(t *testing.T) {
	r := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()
	NewHandler(nil).ListEventCatalog(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var response struct {
		Events []EventCatalogEntry `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) == 0 {
		t.Fatal("catalogue returned no supported events")
	}
	for _, event := range response.Events {
		if event.Name == "" || event.Description == "" || !json.Valid(event.Example) {
			t.Errorf("incomplete catalogue entry: %+v", event)
		}
	}
}

func TestListEventCatalogSearchesNameAndDescription(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  int
	}{
		// "transfers" appears in the batch.completed description, so the
		// substring search matches three entries, not two.
		{"transfer", 3},
		{"confirmed on Stellar", 1},
		{"no-such-event", 0},
	} {
		query := url.Values{"q": []string{tc.query}}
		r := httptest.NewRequest("GET", "/events?"+query.Encode(), nil)
		w := httptest.NewRecorder()
		NewHandler(nil).ListEventCatalog(w, r)
		var response struct {
			Events []EventCatalogEntry `json:"events"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Events) != tc.want {
			t.Errorf("q=%q returned %d events, want %d", tc.query, len(response.Events), tc.want)
		}
	}
}
