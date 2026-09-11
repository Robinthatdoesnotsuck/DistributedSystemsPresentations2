package models

import (
	"database/sql"
	"fmt"
)

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type UserModel struct {
	DB *sql.DB
}

func (m *UserModel) GetByID(id int) (*User, error) {
	user := &User{}
	query := `SELECT id, name, email FROM users WHERE id = $1`

	err := m.DB.QueryRow(query, id).Scan(&user.ID, &user.Name, &user.Email)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}

	return user, nil
}
