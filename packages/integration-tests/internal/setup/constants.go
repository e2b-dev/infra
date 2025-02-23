package setup

import (
	"os"
	"time"
)

const (
	APIServerURL = "http://localhost:3000"
	apiTimeout   = 120 * time.Second

	SandboxTemplateID = "2j6ly824owf4awgai1xo"
)

var (
	APIKey = os.Getenv("E2B_API_KEY")
)
