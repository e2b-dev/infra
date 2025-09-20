package testhacks

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func PrintTestName(c *gin.Context) {
	testName := c.GetHeader("x-test-name")
	if testName != "" {
		ctx := addTestName(c.Request.Context(), testName)
		c.Request = c.Request.WithContext(ctx)
		fmt.Printf("====================== START api request for %s ========================", testName)
	}

	c.Next()

	if testName != "" {
		fmt.Printf("====================== FINISH api call for %s ========================", testName)
	}
}
