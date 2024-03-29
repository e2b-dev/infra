// Package api provides primitives to interact with the openapi HTTP API.
//
// Code generated by github.com/deepmap/oapi-codegen version v1.16.2 DO NOT EDIT.
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
	"BaxvKAAA",
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
