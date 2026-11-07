package entity

import "github.com/smatify/tenet"

// AuthorRepository holds custom queries for Author.
type AuthorRepository struct {
	*tenet.Repository[Author]
}

// NewAuthorRepository returns the repository of Author.
func NewAuthorRepository(em *tenet.EntityManager) *AuthorRepository {
	return &AuthorRepository{tenet.RepositoryFor[Author](em)}
}
