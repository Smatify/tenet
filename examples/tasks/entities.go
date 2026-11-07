package main

import (
	"time"

	"github.com/smatify/tenet"
)

// Every row belongs to a workspace; a filter on the workspace column keeps
// the workspaces apart (see server.em).

// User can be assigned to tasks.
type User struct {
	tenet.Entity `tenet:"table:app_user"`
	tenet.IntID
	Workspace string `tenet:"length:50,index"`
	Name      string `tenet:"length:100"`
}

// Task is deleted softly: Remove sets deleted_at and queries hide it.
type Task struct {
	tenet.Entity `tenet:"table:task"`
	tenet.UUIDID
	tenet.Timestamps
	tenet.SoftDelete
	Workspace string `tenet:"length:50,index"`
	Title     string `tenet:"length:200"`
	Done      bool
	Due       *time.Time
	Assignee  *User
}
