package core

import "mailhearth/internal/purelymail"

func modifyPassword(user, pw string) purelymail.ModifyUserRequest {
	return purelymail.ModifyUserRequest{UserName: user, NewPassword: &pw}
}
