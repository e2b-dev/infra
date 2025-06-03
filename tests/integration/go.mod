module github.com/e2b-dev/infra/tests/integration

go 1.24.3

replace github.com/e2b-dev/infra/packages/shared => ../../packages/shared

require (
	connectrpc.com/connect v1.18.1
	github.com/e2b-dev/infra/packages/shared v0.0.0
	github.com/google/uuid v1.6.0
	github.com/oapi-codegen/runtime v1.1.1
	github.com/stretchr/testify v1.10.0
	golang.org/x/net v0.40.0
	google.golang.org/grpc v1.72.1
	google.golang.org/protobuf v1.36.6
)

require (
	ariga.io/atlas v0.15.0 // indirect
	entgo.io/ent v0.12.5 // indirect
	github.com/agext/levenshtein v1.2.3 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/bitfield/gotestdox v0.2.2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dchest/uniuri v1.2.0 // indirect
	github.com/dnephin/pflag v1.0.7 // indirect
	github.com/dprotaso/go-yit v0.0.0-20220510233725-9ba8df137936 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/fsnotify/fsnotify v1.8.0 // indirect
	github.com/getkin/kin-openapi v0.131.0 // indirect
	github.com/go-openapi/inflect v0.19.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/hashicorp/hcl/v2 v2.19.1 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/oapi-codegen/oapi-codegen/v2 v2.4.1 // indirect
	github.com/oasdiff/yaml v0.0.0-20250309154309-f31be36b4037 // indirect
	github.com/oasdiff/yaml3 v0.0.0-20250309153720-d2182401db90 // indirect
	github.com/onsi/ginkgo v1.16.5 // indirect
	github.com/onsi/gomega v1.36.3 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.13.1 // indirect
	github.com/speakeasy-api/openapi-overlay v0.9.0 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/vmware-labs/yaml-jsonpath v0.3.2 // indirect
	github.com/zclconf/go-cty v1.14.1 // indirect
	golang.org/x/mod v0.24.0 // indirect
	golang.org/x/sync v0.14.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/term v0.32.0 // indirect
	golang.org/x/text v0.25.0 // indirect
	golang.org/x/tools v0.33.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250519155744-55703ea1f237 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gotest.tools/gotestsum v1.12.2 // indirect
)

tool (
	github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
	gotest.tools/gotestsum
)
