package consumer

import (
	"context"

	"github.com/free5gc/openapi"
	"github.com/free5gc/openapi/models"
	"github.com/free5gc/openapi/oauth"
	smf_context "github.com/free5gc/smf/internal/context"
	"github.com/free5gc/smf/internal/logger"
)

func GetTokenCtx(scope, targetNF string) (context.Context, *models.ProblemDetails, error) {
	if smf_context.GetSelf().OAuth2Required {
		logger.ConsumerLog.Debugln("GetToekenCtx")
		smfSelf := smf_context.GetSelf()
		tok, pd, err := oauth.SendAccTokenReq(smfSelf.NfInstanceID, models.NfType_SMF, scope, targetNF, smfSelf.NrfUri)
		if err != nil {
			return nil, pd, err
		}
		return context.WithValue(context.Background(),
			openapi.ContextOAuth2, tok), pd, nil
	}
	return context.TODO(), nil, nil
}
