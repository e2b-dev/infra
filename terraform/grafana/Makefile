tf_vars := TF_VAR_project=$(GCP_PROJECT_ID) 

.PHONY: plan
plan:
	@ printf "Planning Terraform\n"
	terraform fmt -recursive
	terraform plan -out=.tfplan -compact-warnings -detailed-exitcode



.PHONY: apply
apply:
	@ printf "Applying Terraform\n"
	terraform apply \
	-input=false \
	-compact-warnings \
	-parallelism=20 \
	.tfplan
	@ rm .tfplan