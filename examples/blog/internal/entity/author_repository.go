package entity

import "github.com/smatify/smorm"

// AuthorRepository holds custom queries for Author.
type AuthorRepository struct {
	*smorm.Repository[Author]
}

// NewAuthorRepository returns the repository of Author.
func NewAuthorRepository(em *smorm.EntityManager) *AuthorRepository {
	return &AuthorRepository{smorm.RepositoryFor[Author](em)}
}
