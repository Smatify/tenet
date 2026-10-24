CREATE TABLE author (
	id   BIGSERIAL PRIMARY KEY,
	name VARCHAR(100) NOT NULL
);

CREATE TABLE post (
	id        BIGSERIAL PRIMARY KEY,
	author_id BIGINT NOT NULL REFERENCES author (id),
	title     VARCHAR(200) NOT NULL,
	views     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX post_author_id ON post (author_id);
