package health

type Status string

const (
	Healthy   Status = "healthy"
	Unhealthy Status = "unhealthy"
	Draining  Status = "draining"
)

type Response struct {
	Status  Status `json:"status"`
	Version string `json:"version"`
}
