package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
)

func TestReceiptScanRejectsTooManyFiles(t *testing.T) {
	files := make([]scanUploadFile, maxReceiptUploadFiles+1)
	for i := range files {
		files[i] = scanUploadFile{
			filename:    fmt.Sprintf("page-%d.jpg", i+1),
			contentType: "image/jpeg",
			body:        []byte("not decoded because count fails first"),
		}
	}

	c, rec := newScanMultipartContext(t, files)
	err := (&ReceiptHandler{}).Scan(c)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "maximum 10 pages per receipt") {
		t.Fatalf("expected max-page error, got %s", rec.Body.String())
	}
}

func TestReceiptScanRejectsDirectPDFPayloads(t *testing.T) {
	c, rec := newScanMultipartContext(t, []scanUploadFile{{
		filename:    "receipt.pdf",
		contentType: "application/pdf",
		body:        []byte("%PDF-1.7"),
	}})

	err := (&ReceiptHandler{}).Scan(c)
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "only JPEG and PNG are allowed") {
		t.Fatalf("expected unsupported type error, got %s", rec.Body.String())
	}
}

func TestParseReceiptPageSourcesIgnoresMalformedMetadata(t *testing.T) {
	_, provided, reason := parseReceiptPageSources(map[string][]string{
		"page_sources": {"not-json ["},
	})
	if !provided {
		t.Fatalf("expected page_sources to be treated as provided")
	}
	if reason != "" {
		t.Fatalf("plain repeated-value metadata should not be treated as malformed, got reason %q", reason)
	}

	_, provided, reason = parseReceiptPageSources(map[string][]string{
		"page_sources": {"[not-json"},
	})
	if !provided {
		t.Fatalf("expected JSON-looking page_sources to be treated as provided")
	}
	if reason != "invalid_json" {
		t.Fatalf("reason = %q, want invalid_json", reason)
	}
}

func TestParseReceiptPageSourcesNormalizesMetadata(t *testing.T) {
	sources, provided, reason := parseReceiptPageSources(map[string][]string{
		"page_sources": {`["photo","pdf_rendered","future_source"]`},
	})
	if !provided {
		t.Fatalf("expected page_sources to be treated as provided")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
	want := []string{"photo", "pdf_rendered", "unknown"}
	if fmt.Sprint(sources) != fmt.Sprint(want) {
		t.Fatalf("sources = %#v, want %#v", sources, want)
	}

	sources, provided, reason = parseReceiptPageSources(map[string][]string{
		"page_sources": {"photo", "pdf_rendered", "unexpected"},
	})
	if !provided {
		t.Fatalf("expected repeated page_sources to be treated as provided")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
	if fmt.Sprint(sources) != fmt.Sprint(want) {
		t.Fatalf("repeated sources = %#v, want %#v", sources, want)
	}
}

func TestAllReceiptPageSourcesPhoto(t *testing.T) {
	if !allReceiptPageSourcesPhoto([]string{"photo", "photo"}) {
		t.Fatalf("all-photo sources should be recognized")
	}
	if allReceiptPageSourcesPhoto([]string{"photo", "pdf_rendered"}) {
		t.Fatalf("mixed sources should not be treated as all-photo")
	}
	if allReceiptPageSourcesPhoto(nil) {
		t.Fatalf("empty source list should not be treated as all-photo")
	}
}

type scanUploadFile struct {
	filename    string
	contentType string
	body        []byte
}

func newScanMultipartContext(t *testing.T, files []scanUploadFile) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, file := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="images"; filename="%s"`, file.filename))
		header.Set("Content-Type", file.contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatalf("CreatePart: %v", err)
		}
		if _, err := part.Write(file.body); err != nil {
			t.Fatalf("part.Write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/receipts/scan", &body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKeyHouseholdID, "household-id")
	c.Set(auth.ContextKeyUserID, "user-id")
	return c, rec
}
