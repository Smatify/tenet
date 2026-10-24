-- name: InsertPost :one
INSERT INTO post (author_id, title, views) VALUES ($1, $2, $3) RETURNING id;

-- name: GetPost :one
SELECT * FROM post WHERE id = $1;

-- name: ListPostsWithAuthor :many
SELECT sqlc.embed(post), sqlc.embed(author) FROM post JOIN author ON author.id = post.author_id ORDER BY post.id LIMIT $1;

-- name: ListPosts :many
SELECT * FROM post ORDER BY id LIMIT $1;

-- name: UpdatePostViews :exec
UPDATE post SET views = $2 WHERE id = $1;
