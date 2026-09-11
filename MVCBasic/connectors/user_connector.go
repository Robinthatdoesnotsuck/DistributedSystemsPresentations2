package connectors

import (
	"net/http"

	"danny.com/mvc/controllers"
)

type UserConnector struct {
	UserController *controllers.UserController
}

func (co *UserConnector) GetUserHandler(w http.ResponseWriter, r *http.Request) {
}
