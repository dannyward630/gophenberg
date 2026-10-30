// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/gopherium/gouncer/authkit"

	"github.com/gopherium/gophenberg/internal/content"
)

// postCountsResponse is how many published and private posts each account wrote, by account id.
type postCountsResponse struct {
	Counts map[uuid.UUID]int `json:"counts"`
}

// handleUserPostCounts returns an http.HandlerFunc counting the published and private posts each account wrote.
func (s *server) handleUserPostCounts() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counts, err := s.postCounts(r.Context())
		if err != nil {
			respondDomainError(w, err)
			return
		}
		authkit.Respond(w, http.StatusOK, postCountsResponse{Counts: counts})
	}
}

// postCounts returns how many published and private posts each account wrote, none without an active post type.
func (s *server) postCounts(ctx context.Context) (map[uuid.UUID]int, error) {
	posts, err := s.types.Active(ctx, content.TypePost)
	if errors.Is(err, content.ErrTypeNotFound) || errors.Is(err, content.ErrTypeInactive) {
		return map[uuid.UUID]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	return s.content.CountsByAuthor(ctx, posts.Key)
}
