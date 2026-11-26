package quota

import (
	"net/http"
	"strings"
	"testing"
)

func TestPortalForbiddenUsesMessagePage(t *testing.T) {
	h := &portalOutbound{manager: newTestPortalManager()}
	response := servePortalRequest(t, h, "", "", "GET / HTTP/1.1\r\nHost: quota.local\r\n\r\n")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %s", response.Status)
	}
	body := readResponseBody(t, response)
	for _, want := range []string{`<h1 data-i18n="Forbidden">Forbidden</h1>`, `"Forbidden":"无权访问"`, `href="/"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected message page to contain %q, got:\n%s", want, body)
		}
	}
}

func TestMessagePageEscapesDetail(t *testing.T) {
	page := renderMessagePage("Failed to save", `<script>alert(1)</script>`, "")
	if strings.Contains(page, "<script>alert(1)") || !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("detail must be escaped, got:\n%s", page)
	}
}
