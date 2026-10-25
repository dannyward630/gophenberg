// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"syscall"
	"time"

	"github.com/gopherium/framework/gonsole"
	"github.com/jackc/pgx/v5/pgconn"
)

// databaseWaitSetting names the setting that bounds how long serve waits for a database that is not up yet.
const databaseWaitSetting = "DATABASE_WAIT"

// cannotConnectNow is the SQLSTATE PostgreSQL answers while it starts up, shuts down or recovers.
const cannotConnectNow = "57P03"

// waitingLine is what serve logs once it starts to wait for a database that is not up yet.
const waitingLine = "waiting for the database to answer"

// answeredLine is what serve logs once the database it waited for answers.
const answeredLine = "the database answered"

// databaseWait is how long serve waits for a database that is not up yet, and the pause between two tries.
type databaseWait struct {
	limit    time.Duration
	interval time.Duration
}

// databaseWaitFrom reads how long serve waits for its database, 0 waiting not at all, and the pause between two tries.
func databaseWaitFrom(env gonsole.Env) (databaseWait, error) {
	limit, err := env.Duration(databaseWaitSetting, time.Minute, gonsole.AllowZero())
	if err != nil {
		return databaseWait{}, err
	}
	interval, err := env.Duration("DATABASE_WAIT_INTERVAL", time.Second)
	if err != nil {
		return databaseWait{}, err
	}
	return databaseWait{limit: limit, interval: interval}, nil
}

// await pings until the database answers, ctx ends or the wait runs out, logging when it waits and when it answered.
func (w databaseWait) await(ctx context.Context, ping func(context.Context) error, logger *slog.Logger) error {
	started := time.Now()
	err := ping(ctx)
	if !notUpYet(err) {
		return err
	}
	if w.limit > 0 {
		logger.Warn(waitingLine, "error", err, "wait", w.limit)
	}
	if err := w.retry(ctx, ping, started.Add(w.limit), err); err != nil {
		return err
	}
	logger.Info(answeredLine, "after", time.Since(started).Round(time.Millisecond))
	return nil
}

// retry pings again after each pause while failed says the database is not up yet, until deadline passes or ctx ends.
func (w databaseWait) retry(
	ctx context.Context, ping func(context.Context) error, deadline time.Time, failed error,
) error {
	for notUpYet(failed) {
		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("the database did not answer within %v, the time %s allows: %w",
				w.limit, settingsPrefix+databaseWaitSetting, failed)
		}
		if err := pause(ctx, min(w.interval, left)); err != nil {
			return err
		}
		failed = ping(ctx)
	}
	return failed
}

// pause waits for d, answering the end of ctx when it comes first.
func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// notUpYet reports whether err says the database is not up yet: a refused connection, a missing socket or 57P03.
func notUpYet(err error) bool {
	var answered *pgconn.PgError
	if errors.As(err, &answered) {
		return answered.Code == cannotConnectNow
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}
