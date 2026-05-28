package mail

import (
	"agentic-plugin/emailcheck/src/emailcheck/model"

	"context"
	"strings"
)

const (
	incomingProtocolIMAP = "imap"
	incomingProtocolPOP3 = "pop3"
)

func TestIncomingLogin(ctx context.Context, account model.EmailAccount) (model.EmailAccount, []model.LoginAttempt, error) {
	if IncomingProtocol(account) == incomingProtocolPOP3 {
		err := testPOP3Login(ctx, account)
		attempt := model.LoginAttempt{Username: RedactLoginUsername(account.Username), Success: err == nil}
		if err != nil {
			attempt.Error = err.Error()
		}
		return account, []model.LoginAttempt{attempt}, err
	}
	return testIMAPLogin(ctx, account)
}

func ListIncomingMessages(ctx context.Context, account model.EmailAccount, req model.EmailListRequest) ([]model.EmailMessage, error) {
	if IncomingProtocol(account) == incomingProtocolPOP3 {
		return listPOP3Messages(ctx, account, req)
	}
	return listIMAPMessages(ctx, account, req)
}

func MarkIncomingSeen(ctx context.Context, account model.EmailAccount, req model.EmailListRequest) error {
	if IncomingProtocol(account) == incomingProtocolPOP3 {
		return nil
	}
	return markIMAPMessagesSeen(ctx, account, req)
}

func IncomingProtocol(account model.EmailAccount) string {
	switch strings.ToLower(strings.TrimSpace(account.Protocol)) {
	case incomingProtocolPOP3:
		return incomingProtocolPOP3
	default:
		return incomingProtocolIMAP
	}
}
