package context

import (
	"context"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/oauth"
	"github.com/free5gc/smf/internal/logger"
)

func GetTokenCtx(scope, targetNF string) (context.Context, *models.ProblemDetails, error) {
	if GetSelf().OAuth2Required {
		logger.ConsumerLog.Debugln("GetToekenCtx")
		smfSelf := GetSelf()
		tok, pd, err := oauth.SendAccTokenReq(smfSelf.NfInstanceID, models.NfType_SMF, scope, targetNF, smfSelf.NrfUri)
		if err != nil {
			return nil, pd, err
		}
		return context.WithValue(context.Background(),
			openapi.ContextOAuth2, tok), pd, nil
	}
	return context.TODO(), nil, nil
}
