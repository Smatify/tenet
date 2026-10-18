package mapping

import (
	"slices"
	"testing"
)

func TestToSnake(t *testing.T) {
	for in, want := range map[string]string{
		"User": "user", "BlogPost": "blog_post", "UserID": "user_id", "HTTPServer": "http_server",
		"ID": "id", "address2": "address2", "Address2Line": "address2_line", "createdAt": "created_at",
		"already_snake": "already_snake", "": "", "A": "a", "ÄrgerNis": "ärger_nis",
	} {
		if got := ToSnake(in); got != want {
			t.Errorf("ToSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSnakeCaseStrategy(t *testing.T) {
	var n NamingStrategy = SnakeCase{}
	if n.TableName("BlogPost") != "blog_post" || n.ColumnName("CreatedAt") != "created_at" ||
		n.JoinColumnName("Author", "id") != "author_id" || n.JoinTableName("post", "tag") != "post_tag" {
		t.Fatal("unexpected names")
	}
}

func TestParseTag(t *testing.T) {
	tag, err := ParseTag(" column:email , unique,length:180, default:'a,b''c', cascade:persist|remove ,")
	if err != nil {
		t.Fatal(err)
	}
	if tag.Get("column") != "email" || !tag.Has("unique") || tag.Get("unique") != "" || tag.Get("length") != "180" {
		t.Fatalf("%+v", tag)
	}
	if tag.Get("default") != "'a,b''c'" || Unquote(tag.Get("default")) != "a,b'c" {
		t.Fatalf("default = %q", tag.Get("default"))
	}
	if !slices.Equal(List(tag.Get("cascade")), []string{"persist", "remove"}) || List("") != nil {
		t.Fatal("List")
	}
	if !slices.Equal(tag.Keys(), []string{"column", "unique", "length", "default", "cascade"}) {
		t.Fatalf("keys = %v", tag.Keys())
	}
	if Unquote("plain") != "plain" {
		t.Fatal("Unquote")
	}
	if _, err := ParseTag("a,a"); err == nil {
		t.Fatal("duplicate must fail")
	}
	if _, err := ParseTag("default:'oops"); err == nil {
		t.Fatal("unterminated quote must fail")
	}
}
