package entity

import "github.com/smatify/smorm"

// PostRepository holds custom queries for Post.
type PostRepository struct {
	*smorm.Repository[Post]
}

// NewPostRepository returns the repository of Post.
func NewPostRepository(em *smorm.EntityManager) *PostRepository {
	return &PostRepository{smorm.RepositoryFor[Post](em)}
}
