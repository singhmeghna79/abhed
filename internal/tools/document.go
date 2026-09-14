package tools

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/zlib"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Reading documents that are not plain text.
//
// A deep agent is handed PDFs and Word files constantly — a design doc, an
// architecture review, a vendor spec — and until now `read` refused them as
// binary. That refusal was correct (the raw bytes are noise to a model) but it
// made the whole class of file unusable.
//
// Extraction here is deliberately dependency-free. Abhed ships into air-gapped
// environments, so pulling a CGo PDF library or shelling out to pdftotext would
// mean either a build toolchain on the target or a binary Abhed cannot vouch
// for. The tradeoff is honest: this handles the common cases — uncompressed and
// Flate-compressed PDF text, and the OOXML formats, which are just zipped XML —
// and says plainly when it cannot, rather than returning plausible garbage.

// DocumentKind identifies a format that needs extraction before a model reads it.
type DocumentKind string

const (
	KindPlain DocumentKind = ""
	KindPDF   DocumentKind = "pdf"
	KindDOCX  DocumentKind = "docx"
	KindXLSX  DocumentKind = "xlsx"
	KindPPTX  DocumentKind = "pptx"
)

// DetectKind identifies a document by content, not by extension. A file named
// .txt that is really a PDF should still be read as one, and an extension is
// attacker-controlled when the file arrived over an upload.
func DetectKind(data []byte, filename string) DocumentKind {
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return KindPDF
	}
	// OOXML files are ZIP archives; the part names distinguish them.
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		if r, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
			for _, f := range r.File {
				switch {
				case strings.HasPrefix(f.Name, "word/"):
					return KindDOCX
				case strings.HasPrefix(f.Name, "xl/"):
					return KindXLSX
				case strings.HasPrefix(f.Name, "ppt/"):
					return KindPPTX
				}
			}
		}
	}
	return KindPlain
}

// ExtractText converts a document to plain text for a model to read.
func ExtractText(data []byte, kind DocumentKind) (string, error) {
	switch kind {
	case KindPDF:
		return extractPDF(data)
	case KindDOCX, KindPPTX, KindXLSX:
		return extractOOXML(data, kind)
	}
	return string(data), nil
}

// ---------------------------------------------------------------- OOXML

// extractOOXML pulls the text runs out of a zipped Office document. The
// formats differ only in which parts hold the body text.
func extractOOXML(data []byte, kind DocumentKind) (string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("not a readable Office file: %w", err)
	}

	// Which parts carry text, in the order a reader would encounter them.
	want := func(name string) bool {
		switch kind {
		case KindDOCX:
			return name == "word/document.xml" ||
				strings.HasPrefix(name, "word/header") ||
				strings.HasPrefix(name, "word/footnotes")
		case KindPPTX:
			return strings.HasPrefix(name, "ppt/slides/slide")
		case KindXLSX:
			return name == "xl/sharedStrings.xml" ||
				strings.HasPrefix(name, "xl/worksheets/sheet")
		}
		return false
	}

	var parts []string
	for _, f := range r.File {
		if !want(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		// Bounded: a zip bomb must not exhaust memory just because someone
		// uploaded a file.
		raw, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			continue
		}
		if text := ooxmlText(raw); strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("no readable text found in this %s file", kind)
	}
	return strings.Join(parts, "\n\n"), nil
}

// ooxmlText walks the XML and keeps character data from text elements,
// inserting breaks where the markup implies one. Parsing the XML rather than
// stripping tags with a regexp matters: an <w:instrText> field code or a
// <w:delText> tracked deletion is not body text, and a naive strip would
// silently mix them in.
func ooxmlText(raw []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	// Office files declare namespaces the stdlib decoder is strict about;
	// tolerate what it cannot resolve rather than failing the whole document.
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var b strings.Builder
	var inText, skip bool
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t": // w:t (Word), a:t (PowerPoint) — a text run
				inText = true
			case "instrText", "delText":
				skip = true // field codes and tracked deletions are not content
			case "p", "br", "tab", "tr":
				b.WriteString("\n")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "instrText", "delText":
				skip = false
			}
		case xml.CharData:
			if inText && !skip {
				b.Write(t)
			}
		}
	}
	return collapseBlankLines(b.String())
}

// ---------------------------------------------------------------- PDF

var (
	pdfStreamRe = regexp.MustCompile(`(?s)stream\r?\n(.*?)endstream`)
	// Either a literal string to show, or a line-positioning operator. Matched
	// in one pass so their relative order is preserved — that order is the
	// only line structure a PDF content stream has.
	pdfRunRe = regexp.MustCompile(`(\((?:\\.|[^\\()])*\))|(?:\bT[dD]\b|\bT\*)`)
)

// extractPDF pulls text out of a PDF's content streams.
//
// This is not a full PDF parser and does not pretend to be. It finds content
// streams, inflates the Flate-compressed ones, and reads the text-showing
// operators. That covers documents produced by ordinary word processors and
// export pipelines. It does NOT cover scanned pages (which are images and need
// OCR), unusual encodings, or encrypted files — and it says so rather than
// returning a page of mojibake, because a model cannot tell the difference and
// will confidently summarise noise.
func extractPDF(data []byte) (string, error) {
	if bytes.Contains(data[:min(len(data), 2048)], []byte("/Encrypt")) {
		return "", fmt.Errorf("this PDF is encrypted; decrypt it before reading")
	}

	var out strings.Builder
	streams := pdfStreamRe.FindAllSubmatch(data, -1)
	for _, m := range streams {
		body := m[1]
		if inflated, err := inflate(body); err == nil {
			body = inflated
		}
		// A stream that is not text (an image, a font) yields nothing here,
		// which is the correct outcome rather than an error.
		//
		// Text position operators are what separate lines in a PDF: there are
		// no newlines in the content stream, so "Td" (move to next line) and
		// "TD"/"T*" are the only signal that two runs are not on the same
		// line. Without this every page collapses to one long string.
		for _, m := range pdfRunRe.FindAllSubmatch(body, -1) {
			if lit := m[1]; len(lit) > 0 {
				if text := decodePDFString(lit); text != "" {
					out.WriteString(text)
				}
				continue
			}
			out.WriteString("\n")
		}
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteString("\n")
		}
	}

	text := collapseBlankLines(out.String())

	// Both failure modes below mean the same thing: the literal strings in the
	// content stream are glyph indices for an embedded subset font, not
	// characters, and the mapping back lives in a ToUnicode CMap this parser
	// does not read. That is the common modern PDF — Word exports, LaTeX,
	// resume builders — so failing here would make "read this PDF" work on
	// simple files and fail on most real ones.
	//
	// pypdf implements CMap decoding properly. Falling back to it beats
	// shipping a half-correct parser, and when it is absent the original
	// message still explains what to do.
	if strings.TrimSpace(text) == "" || !mostlyPrintable(text) {
		viaPy, pyErr := extractPDFViaPython(data)
		if pyErr == nil && strings.TrimSpace(viaPy) != "" && mostlyPrintable(viaPy) {
			return collapseBlankLines(viaPy), nil
		}
		// The fallback's own failure is reported rather than swallowed. A
		// message saying only "Abhed cannot decode this" when the real cause
		// is a missing interpreter sends the reader to fix the wrong thing.
		detail := ""
		if pyErr != nil {
			detail = " (fallback unavailable: " + pyErr.Error() + ")"
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("no extractable text — this PDF is probably scanned "+
				"images, which need OCR, or uses an encoding Abhed cannot decode%s", detail)
		}
		return "", fmt.Errorf("extracted text looks like binary noise — this PDF "+
			"uses an embedded encoding Abhed cannot decode%s", detail)
	}
	return text, nil
}

// extractPDFViaPython shells out to pypdf for PDFs the native parser cannot
// decode. It is a fallback, never the first attempt: the Go path needs no
// interpreter and is what an air-gapped bundle relies on.
//
// The PDF is passed on stdin rather than written to a temp file, so nothing
// touches the disk and a read-only rootfs stays workable.
func extractPDFViaPython(data []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", "-c", pypdfScript)
	cmd.Stdin = bytes.NewReader(data)
	// A bare environment: this runs an interpreter over untrusted input, so it
	// inherits nothing it does not need.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp"}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pypdf: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// The PDF is read fully into BytesIO before parsing. A pipe is not seekable,
// and pypdf seeks to find the cross-reference table — passing sys.stdin.buffer
// directly fails with "File or stream is not seekable" on every file.
const pypdfScript = `
import sys, io
try:
    import pypdf
except ImportError:
    sys.exit(2)
try:
    r = pypdf.PdfReader(io.BytesIO(sys.stdin.buffer.read()))
    sys.stdout.write("\n".join((p.extract_text() or "") for p in r.pages))
except Exception as e:
    sys.stderr.write(str(e))
    sys.exit(1)
`

// decodePDFString unescapes a PDF literal string: (Hello\040World).
func decodePDFString(lit []byte) string {
	if len(lit) < 2 {
		return ""
	}
	s := lit[1 : len(lit)-1] // strip the parentheses
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			break
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'b', 'f':
			b.WriteByte(' ')
		case '(', ')', '\\':
			b.WriteByte(s[i])
		default:
			// Octal escape: \ddd
			if s[i] >= '0' && s[i] <= '7' {
				v := 0
				n := 0
				for n < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
					v = v*8 + int(s[i]-'0')
					i++
					n++
				}
				i--
				if v > 0 && v < 256 {
					b.WriteByte(byte(v))
				}
			} else {
				b.WriteByte(s[i])
			}
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- helpers

func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t\r")
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// mostlyPrintable guards against reporting decoded noise as document text.
func mostlyPrintable(s string) bool {
	if s == "" {
		return false
	}
	printable := 0
	total := 0
	for _, r := range s {
		total++
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			printable++
		}
		if total > 4000 {
			break
		}
	}
	return float64(printable)/float64(total) > 0.85
}

// inflate decompresses a Flate-compressed PDF stream, which is how nearly all
// real PDFs store their content.
func inflate(data []byte) ([]byte, error) {
	// Leading whitespace after "stream" is common and breaks the reader.
	data = bytes.TrimLeft(data, "\r\n")
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		// Some producers omit the zlib header and emit a raw deflate stream.
		fr := flate.NewReader(bytes.NewReader(data))
		defer fr.Close()
		return io.ReadAll(io.LimitReader(fr, 64<<20))
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, 64<<20))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
