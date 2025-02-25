// Package api provides primitives to interact with the openapi HTTP API.
//
// Code generated by github.com/oapi-codegen/oapi-codegen/v2 version v2.4.1 DO NOT EDIT.
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

	"H4sIAAAAAAAC/+xcbW/jNvL/KgT//xd3gNf2ptuiDdAXye62F3SzzeWhPWAbHGhpHLErkSpJJTECf/cD",
	"HyRREmXLjuNNin21WYkaDmd+MxzODP2AI57lnAFTEh8+4JwIkoECYf43K2gan7zTf1KGD3FOVIJHmJEM",
	"8GH1doQF/FVQATE+VKKAEZZRAhnRn6lFrodKJSi7wcvlCDMeQy9J93IzipKweMbve4nW7zejqyDLU6L6",
	"ufUGbEJ5qQfLnDMJRspvplP9T8SZAqb0nyTPUxoRRTmb/Ck5089qev8vYI4P8f9NatVN7Fs5eS8EF3aO",
	"GGQkaK6J4EN8TGKkWQSp8HKE30xfP/2cR4VKgClHFYEdpyd/8/STf+QKzXnBYjvjD08/41vO5imNjHy/",
	"3YdOL0DcgijluiwxZ0D19uzqLS/s1C02z65QxAVINOcCqQSQMxA8wnMuMqLwIaZMfXOARzgj9zQrMnz4",
	"/QhnlNm/X49KTFOm4AaMUt+z29+IdRskjqmejKRngucgFLVAb/Lxnt1SwVkGTKFbIiiZpUGeuoZpBaK9",
	"VYN8xGMITKMHI/MusL7uOjKQktz0EQryU5v+J+wmKqlcL0f4FDIuFqfHXZL2TXvNiDJ0erxaG69/OPAV",
	"cvB9aCkf4e7CibEjLFIofkYK6RY6J0Wq8OGcpBICZswzos04TRco1x/JBr9krsCuQNEMeKFqKc04T4Ew",
	"zQ3UAFmJdjfMqEKRmKi1BuIWeVoO7/ju5npOYu2U5hQE4nPDdqlAVH7WVfMIl2vz5fX627awLmkGSHGU",
	"0lsIKVZCxFksx0H1lvqcdrXZgpm3Pg2xjw76LR2nKY+Igvjt2VVXDB+LbGZFUI1DlW8YZivVhw7iNIDx",
	"o0y7oeY0mcW9xjk9HjZVJIAo+InQVK5aSilpOxzNzXhvgoIy9d2b4Ax1YLIOLszaeAchbvIev9thEiQS",
	"BWOU3SDOfMIDxCEVUcVaW9KwuLAj2wCqIi1HqcX9qAmeoKqbSilh+A4UoWnAO5MogfhYx4wBDX6g0mDE",
	"jkImtJSIxi3JUAWZDMRUlYiIEGTxwvACK+SxDiqVQFbB4Nx+Wu4FAWk9HZyMK2noPgybi4qDVoxjnrck",
	"CUx7yU9YAIkXeIRjQaheIb4OSLmm/jYh7CbgJx+9ekdAr+UcZJFB/Iz23S+8b2mZNPEX2KcoCaj+SD8u",
	"Nb9qZ45SCkwNs0U7NkglLyrPvQoHVWRtYpr4KODqjTDvEmANKd7RNEVwn1PRcPIxUfBKKynEVObFjquY",
	"qmLMx4VNjaP0OlH2hujGCwgFm8iGSOQ+GiybzWK8cjSaC56hu4RGCaJNe7J+KV4b4zfO/X52oQKiLwEP",
	"WZ4+S+xcd+zjd6qSU1CCRvKrqTxfU8lqFQ3ahGsSgkbBPfir7X0B23vmmxKw2/g3EJLahFGTkHtRUtFj",
	"qzCRsrU42RHenjUUfPl56v7AbwIxN79BwJRYoDuqEhNTSUWyHBEWo5QyreYmRszDIB39BpXZv55kgiEe",
	"NlI7rxNZWvI10DrbYqqmGlmGm3IIbDKpe9pZluzCYRPfp6XecXwtbs3cHoennocellcsv1gL3cYk2imH",
	"SAkabQgKf3PsO3ZumGeJ8uJKQnwW9aRzC0luAOUgImCK3DT2zHnKiQdBZnhw++UlVyQNZm3Mm5V5mp5T",
	"cQaZZjVI1KU7CwnxRjQ3MZbMU9nj7cXbPTwdNFbZFKRG7iWQLLCf5PQXWAQ2lLMT9BnqJLDSXwc8BpXv",
	"ytNbm8TvCagE6s9Lh+qOey2S3tHQ1rE6MCUZ1O46zI1+PtThhyh0XLkh5zgalcLyV11K9kpCIPcPmUs6",
	"tXL2+nHJSaG/DEk2HrIO93WdGSro+i3KDLG8Wf5dvBCONqAv3oBQxDE8F2ayaWt9ksF9c4/WkaH+WA1z",
	"U165eJ00UyIVkkUUgZTzIrUJP2MDN/RWx6erIqstTh8uplgfFDfWXkciw6JiN/544bLvv87x4afVTFaQ",
	"Xl6PMCvSlMxSsDXk5QhrMV3k5I5tzLoRsPa0T3p+yotZGto4mx7JsUUlsuMRF4izdIGI0T+dpYBmi4C3",
	"8FyV1FLYFsNtOazYarYKZkPiLPJ4C8RZtdlPt9y+/Ki4btEIn4Oc/nz78Dn3Ed0GY0MlDR/jezqT8+26",
	"uw08hRkaEnAdpbpt8dN1p+nBeBUzcBN/KQfloj3ll/low6smOqpS07Y4f72zc9O2+q/y8lWA3VDRuWsN",
	"2f0xeAtnHfPoM4g5TQPBybvqnRcx9U+/jVMz6YO3WRwEgFAo4lmmo3/FEdxDVGjX1jLlOjPfC98dR1Ce",
	"zHzlXhlb7tXuvvy3KQNIiApB1eJCy9zOf2QIXPLPwI4KlRjXAESA+Kl0fHaK/yo9BLsOF0PaDKunSpTK",
	"tViP4oyyBkHTuJUAic1w17r1n1dm4KtLR7d0ATbu1HTMX+tonJ28snFq63u9XMrm3LgbqjSQ8fuDY3R0",
	"doJH+LZM6ODp+PV4qqfjOTCSU3yIvxlPx1PtmolKjIwmCZDUsnEDgd3kX+Y1ihKIPmNDSZguo5MYH+Kf",
	"Qdn3uNV0dmAblJqkHE5sRrAKzrx+sZAJVWQnepBV9YTx2M4TZNlUN0maIjsswPRH9yLE8+CmqsrjDwvF",
	"TBPF8rqboeg2XlWySRdIgCoEg9hb0EYCq5rFVo/Vg3wrMstpo/3TtQ4jFdE74ydM9Ft8XStk8mDrtMte",
	"zfwMyqwBGfT2KeZjWe3120V7pFsPmbgisWbxUXpdp0TXgjBYcVVleUO9uT7GdWPf7EPHI5xzGUoPmZo3",
	"klXoQsoielO1Z1zuTrfGixzzeLFTtTaK+MtuH+2BVUcr1na6LSVgjnWGROy5uHTxknWv7bvRS7La6ZYl",
	"Ar9Do2PnF97LFhJakSD6q4Ayq6c4mtO0jH3q5pV/wPhmjP7AhQTxI5lFfxTT6cF3JM9/zAWP/8D/HKN/",
	"Gyo6rgISJSYlpv9zS9ICJMoKqdAM0NX5BwQs4jHEYx3Taw7M/PW2XP63vwH7er/7Srv95nE7TFd7Bo3T",
	"IWic7nFn8uKnJmprxld4LduORaqakKkWtYL9rgPzQfskXqhuqF02A3CXr2nBanc99o1pux7OL+W6I3vA",
	"u71MjDS828Qru2/o5WwBp/x+lcs7rcZ89Xw79Hx+Y8uunWBTuX8btD9UZe2lhXoKKpAU+YWmae0sO9h+",
	"Zz6r4H3hlco3C/LqInsAST3Bl++aPtM0fRlx18Ddq/cMVe9cswUyVaB+d/NE+tjdmaodwGxyrpJ10/FL",
	"VXOvSU7KFHQvDEoQuBT0AAx8sCO3xsEomLPUvlIF+kokUglRSCa8SGO9y1S6owxlNE2p6/vt2XFMqrSx",
	"43RqLKsvtXRaBOwNJ8Sqks4qLnu4SmlGm1zVjc/T6XTTDuanNC2/D2cbu7LI+lsa17pIz7evIVFdZWK9",
	"4d3+vO0uOka3QUsjQPq7ASYvr1WEz5Tm1kWrI2zFEbKCi72tse9QySymGSqZU0FEmPWA5prJUyrS3Zle",
	"N/aHL6t0AXMBMnHlrKDiz+2QhiHAvQIWm0ZZJc3WWF5+GYiK82rexyJjuzRFs44XF5bhQL3UvTHVUttu",
	"68uh3lM/Q64PzvQWvOs+/p3jb77TW+eaO6ruEZ/9CZEanKRtOS4r2T3Fj7sHpLbMVWjU77fwQ/bDLwS3",
	"lceD5pW355sZc05zb+fPl+FBvfuBYcRegPIvGbZvB47RZfjmDrov3YiXvqV1b6nD4hi9JWlqTigJlTpE",
	"SXiMsiJVNE/BtXTxWxB3girX3XV5+WFks2WGYCHt54CiQghgyu/TdpcLymNQzql+z1EGRBYCGksr/eh4",
	"oE1eVvcuv/we0Ljn2W4304ur3XqtD19ermuld5Po3sfa5ocLHJfXO9krpINmyWlJ/YXHtwpINiDFbYcF",
	"zjyX7sU+c72m6/2RaV27oP1lZNvdR63yqn5WKsRWnQYppRwaVEz9suUxQomMqi3ez2Rs1Sx2vW8wuCrd",
	"owFRyuu5gKLmaED1ksHd6oKlj4enCM2CPZ6DArSDnfPQF6HZ1n8dn5Eoglxtfqrdi7IbbmDyULfZrqzM",
	"2NILIv0wsCMqIFz67bubBRVe5+/wnEOj+9yu4nEB8r4sj6go6S7J9ruuMDr92ZMI++mMt9nDO8h6pwOU",
	"7dr8X0KfwONd8jlYN0PYQIf8MqDx1a8/oV+f2F+hmjy4WxTLFUdkczHA7/cfBC37i0jH1SWN7XE2Wju6",
	"vAoS2BoOwt7CKjDxfh3ihetvUl/s6a0oVS7Srr6vDXqdMi/K6zZ7UWmnjHrCYrivLsCXqY9ZeR2qt+pr",
	"77i37pmGKqz8Rv46n0voKbM+qxpr8y7aRnWzSgzPM6GwgZWYb8VticNCpO7SjDycTEhOx3AwG8dwiz0K",
	"D+0fOpYGas2fVW4+NGfm5fXyfwEAAP//PM/b21haAAA=",
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
