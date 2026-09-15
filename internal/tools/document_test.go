package tools

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildPDF makes a minimal but genuine PDF with an uncompressed content stream.
func buildPDF(body string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>",
		"<< /Length 0 >>\nstream\n" + body + "\nendstream",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	for i, o := range objs {
		b.WriteString("")
		b.WriteString(itoaTest(i+1) + " 0 obj\n" + o + "\nendobj\n")
	}
	b.WriteString("trailer\n<< /Size 5 /Root 1 0 R >>\n%%EOF\n")
	return []byte(b.String())
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func TestDetectKindByContent(t *testing.T) {
	// A file's extension is attacker-controlled when it arrived by upload, so
	// detection must go by content.
	if got := DetectKind([]byte("%PDF-1.7\n..."), "notes.txt"); got != KindPDF {
		t.Errorf("PDF named .txt detected as %q, want pdf", got)
	}
	if got := DetectKind([]byte("plain text"), "report.pdf"); got != KindPlain {
		t.Errorf("plain text named .pdf detected as %q, want plain", got)
	}
}

func TestDetectOOXMLKinds(t *testing.T) {
	for _, tc := range []struct {
		part string
		want DocumentKind
	}{
		{"word/document.xml", KindDOCX},
		{"xl/workbook.xml", KindXLSX},
		{"ppt/slides/slide1.xml", KindPPTX},
	} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(tc.part)
		_, _ = w.Write([]byte("<x/>"))
		_ = zw.Close()
		if got := DetectKind(buf.Bytes(), "f.zip"); got != tc.want {
			t.Errorf("part %s detected as %q, want %q", tc.part, got, tc.want)
		}
	}
}

func TestExtractPDFText(t *testing.T) {
	pdf := buildPDF("BT /F1 12 Tf 72 720 Td (Hello world) Tj 0 -14 Td (Second line) Tj ET")
	text, err := ExtractText(pdf, KindPDF)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "Hello world") || !strings.Contains(text, "Second line") {
		t.Errorf("missing text, got %q", text)
	}
	// Td between the runs means they are separate lines, not one long string.
	if strings.Contains(text, "Hello worldSecond") {
		t.Errorf("line positioning ignored; runs were joined: %q", text)
	}
}

func TestExtractPDFUnescapesLiterals(t *testing.T) {
	pdf := buildPDF(`BT (A\(B\) C\040D) Tj ET`)
	text, err := ExtractText(pdf, KindPDF)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "A(B) C D") {
		t.Errorf("escapes not decoded, got %q", text)
	}
}

// A scanned PDF is images. Returning "" or noise would let a model summarise
// something it never read, so this must be an explicit error.
func TestExtractPDFRefusesWhenThereIsNoText(t *testing.T) {
	pdf := buildPDF("q 100 0 0 100 0 0 cm /Im1 Do Q")
	_, err := ExtractText(pdf, KindPDF)
	if err == nil {
		t.Fatal("a PDF with no text returned success")
	}
	if !strings.Contains(err.Error(), "OCR") {
		t.Errorf("error does not explain the likely cause: %v", err)
	}
}

func TestExtractPDFRefusesEncrypted(t *testing.T) {
	pdf := append([]byte("%PDF-1.4\n<< /Encrypt 9 0 R >>\n"), buildPDF("BT (x) Tj ET")...)
	if _, err := ExtractText(pdf, KindPDF); err == nil {
		t.Error("an encrypted PDF was read without complaint")
	}
}

func buildDOCX(t *testing.T, bodyXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(bodyXML))
	_ = zw.Close()
	return buf.Bytes()
}

func TestExtractDOCX(t *testing.T) {
	doc := buildDOCX(t, `<w:document><w:body>
		<w:p><w:r><w:t>First paragraph</w:t></w:r></w:p>
		<w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
	</w:body></w:document>`)
	text, err := ExtractText(doc, KindDOCX)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "First paragraph") || !strings.Contains(text, "Second paragraph") {
		t.Errorf("missing text: %q", text)
	}
	if strings.Contains(text, "First paragraphSecond") {
		t.Errorf("paragraphs were run together: %q", text)
	}
}

// Field codes and tracked deletions are markup, not body text. A regexp that
// stripped tags would silently mix them into the document the model reads.
func TestExtractDOCXSkipsNonBodyText(t *testing.T) {
	doc := buildDOCX(t, `<w:document><w:body><w:p>
		<w:r><w:t>Real content</w:t></w:r>
		<w:r><w:instrText>HYPERLINK "http://evil.example"</w:instrText></w:r>
		<w:r><w:delText>deleted sentence</w:delText></w:r>
	</w:p></w:body></w:document>`)
	text, err := ExtractText(doc, KindDOCX)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "Real content") {
		t.Errorf("dropped real content: %q", text)
	}
	for _, unwanted := range []string{"HYPERLINK", "deleted sentence"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("included non-body text %q: %s", unwanted, text)
		}
	}
}

func TestExtractOOXMLRefusesEmpty(t *testing.T) {
	doc := buildDOCX(t, `<w:document><w:body></w:body></w:document>`)
	if _, err := ExtractText(doc, KindDOCX); err == nil {
		t.Error("a document with no text returned success")
	}
}

func TestPlainTextPassesThrough(t *testing.T) {
	text, err := ExtractText([]byte("just text"), KindPlain)
	if err != nil || text != "just text" {
		t.Errorf("ExtractText(plain) = %q, %v", text, err)
	}
}

// A PDF whose text is stored as glyph indices for an embedded subset font —
// which is what Word, LaTeX and most resume builders produce — decodes to
// binary noise through the literal-string path. The extractor must recognise
// that and fall back rather than returning the noise as if it were text.
//
// This was found on a real resume: "read this PDF" worked on simple files and
// failed on the common case, which is the worst shape a bug can have.
func TestPDFBinaryNoiseIsNotReturnedAsText(t *testing.T) {
	// A literal string of glyph indices, not characters.
	pdf := []byte("%PDF-1.7\nstream\n" +
		"BT (\x00\x03\x00\x05\x00\x07\xb1\x00\x05) Tj ET\n" +
		"endstream\n")

	text, err := ExtractText(pdf, KindPDF)
	if err == nil && !mostlyPrintable(text) {
		t.Fatalf("binary noise returned as extracted text: %q", text)
	}
	// Either it errors, or a fallback decoded it properly. Silently handing
	// the model unreadable bytes is the one outcome that must not happen.
	if err != nil && !strings.Contains(err.Error(), "encoding") &&
		!strings.Contains(err.Error(), "no extractable text") {
		t.Fatalf("unhelpful error for an undecodable PDF: %v", err)
	}
}
