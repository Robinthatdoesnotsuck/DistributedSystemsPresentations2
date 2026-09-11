package controllers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"danny.com/mvc/models"
)

type UserController struct {
	UserModel *models.UserModel
}

func (c *UserController) GetUserHandler(w http.ResponseWriter, r *http.Request) {
	// Simple query parameter parsing: /user?id=1
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, "Missing 'id' query parameter", http.StatusBadRequest)
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	user, err := c.UserModel.GetByID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// View rendering (in API terms, JSON response representation)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}
