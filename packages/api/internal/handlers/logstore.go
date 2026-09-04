package handlers

import "errors"

// noLogStoreError is nil when at least one store for sandbox and build logs is
// configured. LOKI_URL used to be required, so an api with neither was
// impossible; now that it is optional, starting with neither would only turn
// every log read into a 500, so it is refused at startup instead.
func noLogStoreError(lokiURL, clickhouseConnectionString string) error {
	if lokiURL == "" && clickhouseConnectionString == "" {
		return errors.New("neither LOKI_URL nor CLICKHOUSE_CONNECTION_STRING is set: sandbox and build log reads would have no store")
	}

	return nil
}
