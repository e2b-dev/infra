package consts

import (
	"os"
)

var (
	AzureStorageAccountName      = os.Getenv("AZURE_STORAGE_ACCOUNT_NAME")
	AzureStorageAccountKey       = os.Getenv("AZURE_STORAGE_ACCOUNT_KEY")
	AzureStorageConnectionString = os.Getenv("AZURE_STORAGE_CONNECTION_STRING")
)
