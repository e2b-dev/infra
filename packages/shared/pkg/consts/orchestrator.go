package consts

// OrchestratorRuntimeOCIKataLabel marks an orchestrator whose sandboxes cold
// boot OCI images in Kata VMs. Unlike Firecracker snapshots, those images only
// require CPU architecture compatibility, not an exact host CPU family/model.
const OrchestratorRuntimeOCIKataLabel = "e2b-runtime-oci-kata"

// OrchestratorRuntimeClassMetadataKey lets a sandbox request select an
// operator-advertised Kubernetes RuntimeClass. The API also uses it to keep an
// explicit Kata request away from Firecracker orchestrators in mixed fleets.
const OrchestratorRuntimeClassMetadataKey = "e2b.runtime-class"
