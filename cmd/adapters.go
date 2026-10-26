package cmd

import (
	"context"
	"database/sql"
	"whatsappBot/utils"

	"go.mau.fi/whatsmeow/types"
)

// NoDB wraps a handler that doesn't need a *sql.DB into a full CommandHandlerFunc.
func NoDB(fn func(
	ctx context.Context,
	client utils.MessageSender,
	info types.MessageInfo,
	args []string,
) error) CommandHandlerFunc {

	return func(
		db *sql.DB,
		ctx context.Context,
		client utils.MessageSender,
		info types.MessageInfo,
		args []string,
	) error {
		// just ignore the db parameter
		return fn(ctx, client, info, args)
	}
}
