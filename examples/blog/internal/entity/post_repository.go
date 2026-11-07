package entity

import "github.com/smatify/tenet"

// PostRepository holds custom queries for Post.
type PostRepository struct {
	*tenet.Repository[Post]
}

// NewPostRepository returns the repository of Post.
func NewPostRepository(em *tenet.EntityManager) *PostRepository {
	return &PostRepository{tenet.RepositoryFor[Post](em)}
}
