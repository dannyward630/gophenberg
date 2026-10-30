// SPDX-License-Identifier: Apache-2.0

package app

import (
	"cmp"
	"errors"
	"fmt"

	"github.com/gopherium/framework/goncierge"
	"github.com/gopherium/framework/gonsole"
	"github.com/gopherium/gouncer/avatar"

	"github.com/gopherium/gophenberg/internal/accountbridge"
	"github.com/gopherium/gophenberg/internal/contentbridge"
	"github.com/gopherium/gophenberg/internal/role"
	"github.com/gopherium/gophenberg/sdk"
)

// errNamesSentOut reports the Gravatar default image that would send letters of each name to Gravatar.
var errNamesSentOut = errors.New("this default sends names to Gravatar, the initials mode draws them on the site")

// compose builds the stores the address picks, the plugins, their roles and their share off SQLite, starting nothing.
func compose(cfg composeConfig, plugins func(sdk.Deps) ([]sdk.Plugin, error)) (site, error) {
	lane, err := laneSettingsFrom(cfg.getenv)
	if err != nil {
		return site{}, err
	}
	avatars, err := avatarsFrom(cfg.getenv)
	if err != nil {
		return site{}, err
	}
	built, err := openStores(cfg)
	if err != nil {
		return site{}, err
	}
	built.lane, built.avatars = &pluginLane{sqlite: onSQLite(cfg.databaseURL)}, avatars
	built.roles = cmp.Or(cfg.roles, goncierge.New())
	built.defaultRole = valueOr(settingsEnv(cfg.getenv), defaultRoleSetting, role.Member)
	accounts := accountbridge.New(built.users, accountbridge.Options{
		Avatar: avatarOf(avatars), Timeout: lane.queryTimeout,
	})
	built.registered, built.failed = plugins(pluginDeps(cfg, built, accounts))
	if err := built.ready(lane); err != nil {
		built.close()
		return site{}, errors.Join(built.failed, err)
	}
	accounts.Open()
	return built, nil
}

// avatarsFrom reads how the site draws avatars, each setting with its WordPress default.
func avatarsFrom(getenv func(string) string) (avatar.Options, error) {
	env := settingsEnv(getenv)
	mode, err := gonsole.Parse(env, "AVATARS", avatar.ModeInitials, avatar.ParseMode)
	if err != nil {
		return avatar.Options{}, err
	}
	fallback, err := gonsole.Parse(env, "AVATAR_DEFAULT", avatar.DefaultMystery, parseAvatarDefault)
	if err != nil {
		return avatar.Options{}, err
	}
	rating, err := gonsole.Parse(env, "AVATAR_RATING", avatar.RatingG, avatar.ParseRating)
	if err != nil {
		return avatar.Options{}, err
	}
	return avatar.Options{Mode: mode, Default: fallback, Rating: rating}, nil
}

// parseAvatarDefault returns the Gravatar default image value names, refusing the one that sends names out.
func parseAvatarDefault(value string) (avatar.Default, error) {
	fallback, err := avatar.ParseDefault(value)
	if err != nil {
		return "", err
	}
	if fallback == avatar.DefaultInitials {
		return "", fmt.Errorf("%w, got %q", errNamesSentOut, value)
	}
	return fallback, nil
}

// avatarOf returns the avatar builder the account reader draws with, under the site's avatar options.
func avatarOf(options avatar.Options) func(name, email string, size int) string {
	return func(name, email string, size int) string {
		return avatar.Address(name, email, size, options)
	}
}

// ready declares the core's and the registered plugins' roles, checks the default role, then opens the plugins' share.
func (s site) ready(lane laneSettings) error {
	if err := declareRoles(s.roles, role.Core(), s.registered); err != nil {
		return err
	}
	if err := checkDefaultRole(s.roles, s.defaultRole); err != nil {
		return err
	}
	if s.pgHandle == nil {
		return nil
	}
	return s.lane.open(s.pgHandle, len(s.registered), lane)
}

// close releases the PostgreSQL handle and with it the plugins' share, nothing on SQLite or while describing.
func (s site) close() {
	s.release()
}

// pluginDeps returns what the site lends every plugin, a settings reader that is never nil and no address on SQLite.
func pluginDeps(cfg composeConfig, built site, accounts sdk.AccountReader) sdk.Deps {
	getenv := cfg.getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	databaseURL := cfg.databaseURL
	if onSQLite(databaseURL) {
		databaseURL = ""
	}
	return sdk.Deps{
		DatabaseURL: databaseURL,
		DB:          built.lane,
		Content:     contentbridge.New(built.content, built.registry, built.library, built.settings),
		Accounts:    accounts,
		Getenv:      getenv,
		Env:         sdk.Env{Prefix: settingsPrefix, Getenv: getenv},
	}
}
