package testhacks

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

func ProcessGinRequest(c *gin.Context) {
	testName := c.GetHeader("x-test-name")
	if testName == "" {
		c.Next()
		return
	}

	ctx := addTestName(c.Request.Context(), testName)
	c.Request = c.Request.WithContext(ctx)
	fmt.Printf("====================== START client-proxy request for %s ========================", testName)

	c.Next()

	fmt.Printf("====================== FINISH client-proxy call for %s ========================", testName)
}
