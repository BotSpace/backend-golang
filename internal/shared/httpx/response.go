package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// OK writes a successful JSON response.
func OK(c *gin.Context, status int, payload any) {
	c.JSON(status, payload)
}

// Created is shorthand for OK with 201.
func Created(c *gin.Context, payload any) {
	c.JSON(http.StatusCreated, payload)
}

// NoContent writes 204 with empty body.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Error writes an error response, mapping AppError to its status. Unknown
// errors become 500. Always call c.Error first if you want middleware to see it.
func Error(c *gin.Context, err error) {
	_ = c.Error(err)

	ae, ok := AsAppError(err)
	if !ok {
		ae = Internal(err)
	}
	c.AbortWithStatusJSON(ae.Status, ErrorResponse{
		Error: ErrorBody{Code: ae.Code, Message: ae.Message},
	})
}

// ErrorWithDetails writes an error response with extra structured details
// (e.g. validation field errors).
func ErrorWithDetails(c *gin.Context, err error, details map[string]any) {
	_ = c.Error(err)
	ae, ok := AsAppError(err)
	if !ok {
		ae = Internal(err)
	}
	c.AbortWithStatusJSON(ae.Status, ErrorResponse{
		Error: ErrorBody{Code: ae.Code, Message: ae.Message, Details: details},
	})
}
