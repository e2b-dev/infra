package health

type Status string

const (
	Healthy   Status = "healthy"
	Unhealthy Status = "unhealthy"
)

type Response struct {
	Status  Status `json:"status"`
}
