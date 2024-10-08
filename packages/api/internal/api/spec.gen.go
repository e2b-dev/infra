// Package api provides primitives to interact with the openapi HTTP API.
//
// Code generated by github.com/deepmap/oapi-codegen version v1.16.3 DO NOT EDIT.
package api

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Base64 encoded, gzipped, json marshaled Swagger object
var swaggerSpec = []string{

	"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",
}

// GetSwagger returns the content of the embedded swagger specification file
// or error if failed to decode
func decodeSpec() ([]byte, error) {
	zipped, err := base64.StdEncoding.DecodeString(strings.Join(swaggerSpec, ""))
	if err != nil {
		return nil, fmt.Errorf("error base64 decoding spec: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(zipped))
	if err != nil {
		return nil, fmt.Errorf("error decompressing spec: %w", err)
	}
	var buf bytes.Buffer
	_, err = buf.ReadFrom(zr)
	if err != nil {
		return nil, fmt.Errorf("error decompressing spec: %w", err)
	}

	return buf.Bytes(), nil
}

var rawSpec = decodeSpecCached()

// a naive cached of a decoded swagger spec
func decodeSpecCached() func() ([]byte, error) {
	data, err := decodeSpec()
	return func() ([]byte, error) {
		return data, err
	}
}

// Constructs a synthetic filesystem for resolving external references when loading openapi specifications.
func PathToRawSpec(pathToFile string) map[string]func() ([]byte, error) {
	res := make(map[string]func() ([]byte, error))
	if len(pathToFile) > 0 {
		res[pathToFile] = rawSpec
	}

	return res
}

// GetSwagger returns the Swagger specification corresponding to the generated code
// in this file. The external references of Swagger specification are resolved.
// The logic of resolving external references is tightly connected to "import-mapping" feature.
// Externally referenced files must be embedded in the corresponding golang packages.
// Urls can be supported but this task was out of the scope.
func GetSwagger() (swagger *openapi3.T, err error) {
	resolvePath := PathToRawSpec("")

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(loader *openapi3.Loader, url *url.URL) ([]byte, error) {
		pathToFile := url.String()
		pathToFile = path.Clean(pathToFile)
		getSpec, ok := resolvePath[pathToFile]
		if !ok {
			err1 := fmt.Errorf("path not found: %s", pathToFile)
			return nil, err1
		}
		return getSpec()
	}
	var specData []byte
	specData, err = rawSpec()
	if err != nil {
		return
	}
	swagger, err = loader.LoadFromData(specData)
	if err != nil {
		return
	}
	return
}
