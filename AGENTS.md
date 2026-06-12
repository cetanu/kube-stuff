# No Direct Cluster Access
This repository manages a Kubernetes cluster using Infrastructure as Code (CloudFormation) and Configuration Management (SaltStack), deployed via CI/CD (GitHub Actions). 

# Do Not Run `kubectl` or `helm` Locally
You do NOT have direct access to the Kubernetes cluster. Do not attempt to run `kubectl`, `helm`, or `curl` against the cluster API. All changes must be made through modifying the codebase (CloudFormation templates, Salt `.sls` files, etc.).

# Do Not Ask the User to Run Commands
Do not instruct the user to run `kubectl` commands to debug. The user interacts with the cluster via the CI/CD pipeline.

# Troubleshooting
If there is a cluster issue, you must deduce the problem by analyzing the code (CloudFormation, SaltStack states, scripts, and logs provided by the user) rather than trying to probe the cluster dynamically.

# Workflow
- Analyze `.yml` and `.sls` files to understand cluster configuration.
- Propose and make file changes using file editing tools.
- The user will commit and push, triggering the CI/CD pipeline.
