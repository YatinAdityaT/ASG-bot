package cmd

import (
	"context"
	"database/sql"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// NoDB wraps a handler that doesn't need a *sql.DB into a full CommandHandlerFunc.
func NoDB(fn func(
	ctx context.Context,
	client *whatsmeow.Client,
	info types.MessageInfo,
	args []string,
)) CommandHandlerFunc {

	return func(
		db *sql.DB,
		ctx context.Context,
		client *whatsmeow.Client,
		info types.MessageInfo,
		args []string,
	) {
		// just ignore the db parameter
		fn(ctx, client, info, args)
	}
}
