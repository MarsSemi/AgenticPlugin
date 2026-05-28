package model

type EmailAccount struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Email         string          `json:"email"`
	FromName      string          `json:"from_name"`
	Username      string          `json:"username"`
	Password      string          `json:"password,omitempty"`
	DefaultFolder string          `json:"default_folder"`
	Enabled       bool            `json:"enabled"`
	Protocol      string          `json:"incoming_protocol"`
	IMAP          EmailServer     `json:"imap"`
	POP3          EmailServer     `json:"pop3"`
	SMTP          EmailServer     `json:"smtp"`
	LastCheck     EmailCheckState `json:"last_check,omitempty"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
}

type EmailServer struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	TLS                bool   `json:"tls"`
	StartTLS           bool   `json:"start_tls"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

type EmailCheckState struct {
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
}

type EmailAutoCheckTask struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Enabled         bool                 `json:"enabled"`
	AccountID       string               `json:"account_id"`
	Folder          string               `json:"folder"`
	UnreadOnly      bool                 `json:"unread_only"`
	SinceDays       int                  `json:"since_days"`
	Limit           int                  `json:"limit"`
	IntervalMinutes int                  `json:"interval_minutes"`
	SubjectKeywords []string             `json:"subject_keywords,omitempty"`
	FromKeywords    []string             `json:"from_keywords,omitempty"`
	BodyKeywords    []string             `json:"body_keywords,omitempty"`
	Prompt          string               `json:"prompt,omitempty"`
	Line            LineNotifyTarget     `json:"line,omitempty"`
	LastRun         string               `json:"last_run,omitempty"`
	NextRun         string               `json:"next_run,omitempty"`
	LastResult      EmailAutoCheckResult `json:"last_result,omitempty"`
	Metadata        map[string]any       `json:"metadata,omitempty"`
}

type LineNotifyTarget struct {
	Enabled bool   `json:"enabled"`
	RoomID  string `json:"room_id"`
	Note    string `json:"note,omitempty"`
}

type EmailAutoCheckResult struct {
	Status       string   `json:"status,omitempty"`
	Message      string   `json:"message,omitempty"`
	CheckedAt    string   `json:"checked_at,omitempty"`
	ScannedCount int      `json:"scanned_count,omitempty"`
	MatchedCount int      `json:"matched_count,omitempty"`
	MatchedUIDs  []string `json:"matched_uids,omitempty"`
	LineStatus   string   `json:"line_status,omitempty"`
}

type LoginAttempt struct {
	Username string `json:"username"`
	Error    string `json:"error,omitempty"`
	Success  bool   `json:"success"`
}

type EmailListRequest struct {
	AccountID   string   `json:"account_id"`
	Folder      string   `json:"folder"`
	Limit       int      `json:"limit"`
	UnreadOnly  bool     `json:"unread_only"`
	SinceDays   int      `json:"since_days"`
	Since       string   `json:"since"`
	UIDs        []string `json:"uids"`
	IncludeBody bool     `json:"include_body"`
	MarkSeen    bool     `json:"mark_seen"`
}

type EmailReplyRequest struct {
	AccountID string   `json:"account_id"`
	Folder    string   `json:"folder"`
	UID       string   `json:"uid"`
	To        []string `json:"to"`
	CC        []string `json:"cc"`
	BCC       []string `json:"bcc"`
	Subject   string   `json:"subject"`
	Text      string   `json:"text"`
	HTML      string   `json:"html"`
}

type EmailMessage struct {
	UID         string   `json:"uid"`
	Subject     string   `json:"subject"`
	From        string   `json:"from"`
	ReplyTo     string   `json:"reply_to,omitempty"`
	To          []string `json:"to,omitempty"`
	CC          []string `json:"cc,omitempty"`
	Date        string   `json:"date,omitempty"`
	MessageID   string   `json:"message_id,omitempty"`
	InReplyTo   string   `json:"in_reply_to,omitempty"`
	References  string   `json:"references,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	Size        int      `json:"size,omitempty"`
	TextPreview string   `json:"text_preview,omitempty"`
	TextBody    string   `json:"text_body,omitempty"`
	HTMLBody    string   `json:"html_body,omitempty"`
}

type FetchedMessage struct {
	UID   string
	Raw   []byte
	Flags []string
	Size  int
}
