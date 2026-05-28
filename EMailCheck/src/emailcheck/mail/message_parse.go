package mail

import (
	"agentic-plugin/emailcheck/src/emailcheck/model"

	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

func parseEmailMessage(fetched model.FetchedMessage, includeBody bool) (model.EmailMessage, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(fetched.Raw))
	if err != nil {
		return model.EmailMessage{}, err
	}
	decoder := &mime.WordDecoder{CharsetReader: charsetReader}
	header := msg.Header
	subject, _ := decoder.DecodeHeader(header.Get("Subject"))
	from, _ := decoder.DecodeHeader(header.Get("From"))
	replyTo, _ := decoder.DecodeHeader(header.Get("Reply-To"))
	to := decodeAddressHeader(decoder, header.Get("To"))
	cc := decodeAddressHeader(decoder, header.Get("Cc"))
	dateText := header.Get("Date")
	if parsed, err := mail.ParseDate(dateText); err == nil {
		dateText = parsed.Format(time.RFC3339)
	}
	body, _ := io.ReadAll(io.LimitReader(msg.Body, 20<<20))
	contentType := header.Get("Content-Type")
	plain, html := extractMessageBodies(contentType, header.Get("Content-Transfer-Encoding"), body)
	preview := textPreview(firstNonEmpty(plain, stripHTML(html)), 500)
	message := model.EmailMessage{
		UID:         fetched.UID,
		Subject:     strings.TrimSpace(subject),
		From:        strings.TrimSpace(from),
		ReplyTo:     strings.TrimSpace(replyTo),
		To:          to,
		CC:          cc,
		Date:        strings.TrimSpace(dateText),
		MessageID:   strings.TrimSpace(header.Get("Message-ID")),
		InReplyTo:   strings.TrimSpace(header.Get("In-Reply-To")),
		References:  strings.TrimSpace(header.Get("References")),
		Flags:       fetched.Flags,
		Size:        fetched.Size,
		TextPreview: preview,
	}
	if includeBody {
		message.TextBody = plain
		message.HTMLBody = html
	}
	return message, nil
}

func parseMessageDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true
	}
	if parsed, err := mail.ParseDate(value); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func decodeAddressHeader(decoder *mime.WordDecoder, raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	decoded, _ := decoder.DecodeHeader(raw)
	addresses, err := mail.ParseAddressList(decoded)
	if err != nil {
		return []string{decoded}
	}
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, address.String())
	}
	return out
}

func extractMessageBodies(contentType string, transferEncoding string, data []byte) (string, string) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return decodeBody(data, transferEncoding)
	}
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
		var plainParts []string
		var htmlParts []string
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			partType := part.Header.Get("Content-Type")
			partData, _ := io.ReadAll(io.LimitReader(part, 10<<20))
			partPlain, partHTML := extractPartBodies(partType, part.Header.Get("Content-Transfer-Encoding"), partData)
			if partPlain != "" {
				plainParts = append(plainParts, partPlain)
			}
			if partHTML != "" {
				htmlParts = append(htmlParts, partHTML)
			}
		}
		return strings.Join(plainParts, "\n\n"), strings.Join(htmlParts, "\n\n")
	}
	return extractPartBodies(contentType, transferEncoding, data)
}

func extractPartBodies(contentType string, transferEncoding string, data []byte) (string, string) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = "text/plain"
		params = map[string]string{}
	}
	decoded := decodeBodyBytes(data, transferEncoding)
	if strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return extractMessageBodies(contentType, "", decoded)
	}
	text := strings.TrimSpace(decodeCharset(decoded, params["charset"]))
	switch strings.ToLower(mediaType) {
	case "text/html":
		return "", text
	default:
		if strings.HasPrefix(strings.ToLower(mediaType), "text/") {
			return text, ""
		}
	}
	return "", ""
}

func decodeBody(data []byte, transferEncoding string) (string, string) {
	return strings.TrimSpace(decodeCharset(decodeBodyBytes(data, transferEncoding), "")), ""
}

func decodeBodyBytes(data []byte, transferEncoding string) []byte {
	switch strings.ToLower(strings.TrimSpace(transferEncoding)) {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(compactBase64(data))
		if err == nil {
			return decoded
		}
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		if err == nil {
			return decoded
		}
	}
	return data
}

func compactBase64(data []byte) string {
	text := strings.TrimSpace(string(data))
	text = strings.ReplaceAll(text, "\r", "")
	text = strings.ReplaceAll(text, "\n", "")
	text = strings.ReplaceAll(text, "\t", "")
	text = strings.ReplaceAll(text, " ", "")
	return text
}

func decodeCharset(data []byte, charset string) string {
	charset = normalizeCharset(charset)
	if charset == "" || charset == "utf-8" || charset == "utf8" || charset == "us-ascii" {
		if charset == "" && !utf8.Valid(data) {
			if decoded, ok := decodeWithEncoding(data, traditionalchinese.Big5); ok {
				return decoded
			}
			if decoded, ok := decodeWithEncoding(data, simplifiedchinese.GB18030); ok {
				return decoded
			}
		}
		return string(data)
	}
	enc, err := charsetEncoding(charset)
	if err != nil || enc == nil {
		if utf8.Valid(data) {
			return string(data)
		}
		return string([]rune(string(data)))
	}
	if decoded, ok := decodeWithEncoding(data, enc); ok {
		return decoded
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return string([]rune(string(data)))
}

func decodeWithEncoding(data []byte, enc encoding.Encoding) (string, bool) {
	decoded, err := io.ReadAll(transform.NewReader(bytes.NewReader(data), enc.NewDecoder()))
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	enc, err := charsetEncoding(normalizeCharset(charset))
	if err != nil || enc == nil {
		return input, nil
	}
	return transform.NewReader(input, enc.NewDecoder()), nil
}

func charsetEncoding(charset string) (encoding.Encoding, error) {
	switch charset {
	case "big5", "big-5", "csbig5":
		return traditionalchinese.Big5, nil
	case "gbk", "cp936", "windows-936":
		return simplifiedchinese.GBK, nil
	case "gb2312", "hz-gb-2312":
		return simplifiedchinese.HZGB2312, nil
	case "gb18030":
		return simplifiedchinese.GB18030, nil
	case "windows-950", "cp950", "ms950":
		return traditionalchinese.Big5, nil
	}
	if enc, err := htmlindex.Get(charset); err == nil {
		return enc, nil
	}
	if enc, ok := legacyCharmap(charset); ok {
		return enc, nil
	}
	return nil, mime.ErrInvalidMediaParameter
}

func legacyCharmap(charset string) (encoding.Encoding, bool) {
	switch charset {
	case "iso-8859-1", "latin1", "latin-1":
		return charmap.ISO8859_1, true
	case "windows-1250", "cp1250":
		return charmap.Windows1250, true
	case "windows-1251", "cp1251":
		return charmap.Windows1251, true
	case "windows-1252", "cp1252":
		return charmap.Windows1252, true
	case "windows-1253", "cp1253":
		return charmap.Windows1253, true
	case "windows-1254", "cp1254":
		return charmap.Windows1254, true
	case "windows-1255", "cp1255":
		return charmap.Windows1255, true
	case "windows-1256", "cp1256":
		return charmap.Windows1256, true
	case "windows-1257", "cp1257":
		return charmap.Windows1257, true
	case "windows-1258", "cp1258":
		return charmap.Windows1258, true
	default:
		return nil, false
	}
}

func normalizeCharset(charset string) string {
	charset = strings.ToLower(strings.TrimSpace(charset))
	charset = strings.Trim(charset, "\"'")
	return strings.ReplaceAll(charset, "_", "-")
}

func textPreview(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func stripHTML(input string) string {
	if input == "" {
		return ""
	}
	re := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	out := re.ReplaceAllString(input, " ")
	re = regexp.MustCompile(`(?s)<[^>]+>`)
	out = re.ReplaceAllString(out, " ")
	out = strings.ReplaceAll(out, "&nbsp;", " ")
	out = strings.ReplaceAll(out, "&lt;", "<")
	out = strings.ReplaceAll(out, "&gt;", ">")
	out = strings.ReplaceAll(out, "&amp;", "&")
	return strings.Join(strings.Fields(out), " ")
}
