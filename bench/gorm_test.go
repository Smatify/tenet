package bench

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type gormAuthor struct {
	ID   int64
	Name string
}

func (gormAuthor) TableName() string { return "author" }

type gormPost struct {
	ID       int64
	AuthorID int64
	Author   *gormAuthor
	Title    string
	Views    int32
}

func (gormPost) TableName() string { return "post" }

func init() {
	var gdb *gorm.DB
	setups = append(setups, func(db *sql.DB) (err error) {
		gdb, err = gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		return err
	})
	impls = append(impls, impl{
		name: "gorm",
		insert: func(ctx context.Context, n int) error {
			posts := make([]gormPost, n)
			for i := range posts {
				posts[i] = gormPost{AuthorID: 1, Title: fmt.Sprint("new ", i)}
			}
			if n == 1 {
				return gdb.WithContext(ctx).Create(&posts[0]).Error
			}
			return gdb.WithContext(ctx).Create(&posts).Error
		},
		find: func(ctx context.Context, id int64) (string, error) {
			var p gormPost
			err := gdb.WithContext(ctx).First(&p, id).Error
			return p.Title, err
		},
		load100: func(ctx context.Context) ([]string, error) {
			var posts []gormPost
			err := gdb.WithContext(ctx).Preload("Author").Order("id").Limit(100).Find(&posts).Error
			names := make([]string, len(posts))
			for i, p := range posts {
				names[i] = p.Author.Name
			}
			return names, err
		},
		update100: func(ctx context.Context) error {
			var posts []gormPost
			if err := gdb.WithContext(ctx).Order("id").Limit(100).Find(&posts).Error; err != nil {
				return err
			}
			return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				for i := range posts {
					if err := tx.Model(&posts[i]).Update("views", posts[i].Views+1).Error; err != nil {
						return err
					}
				}
				return nil
			})
		},
	})
}
