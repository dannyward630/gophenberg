// SPDX-License-Identifier: Apache-2.0

package features_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
)

// postCountsPath is the route answering how many posts each account wrote.
const postCountsPath = "/api/users/post-counts"

// thePrivatePost stores a post and makes it private.
func thePrivatePost(ctx context.Context, title string) error {
	if err := thePostExists(ctx, title); err != nil {
		return err
	}
	w, err := worldOf(ctx)
	if err != nil {
		return err
	}
	stored := w.nested[title]
	body := fmt.Sprintf(`{"updated_at":%q,"status":"private"}`, stored.UpdatedAt)
	if err := w.patchJSON(contentPath+"/"+stored.ID, body); err != nil {
		return err
	}
	return w.expect(http.StatusOK)
}

// theAccountAsksForThePostCounts asks how many posts each account wrote.
func theAccountAsksForThePostCounts(ctx context.Context) error {
	return theAccountAsksFor(ctx, postCountsPath)
}

// thePostCountsHold asserts the post counts hold exactly the two counts, each under the account at its email.
func thePostCountsHold(ctx context.Context, first int, firstEmail string, second int, secondEmail string) error {
	w, err := worldOf(ctx)
	if err != nil {
		return err
	}
	if err := w.expect(http.StatusOK); err != nil {
		return fmt.Errorf("asking for the post counts: %w", err)
	}
	var answered struct {
		Counts map[string]int `json:"counts"`
	}
	if err := w.answer.decode(&answered); err != nil {
		return err
	}
	want := map[string]int{}
	for email, count := range map[string]int{firstEmail: first, secondEmail: second} {
		id, err := accountID(ctx, email)
		if err != nil {
			return err
		}
		want[id] = count
	}
	if !maps.Equal(answered.Counts, want) {
		return fmt.Errorf("the post counts hold %v, want %v", answered.Counts, want)
	}
	return nil
}
