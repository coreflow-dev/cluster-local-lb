# Cluster Local Loadbalancer

Access and manage (child) clusters via their kubernetes api without creating external load balancers.
Instead this automatically fetches the control plane ips of your child cluster (e.g. from capi) and
creates a normal k8s service targeting them.
Additionally the project also provides health checking for these endpoints.

**Advantages:**
- no need for a load balancer

**Drawbacks:**
- CPU/resource usage on the "parent" cluster
- load balanced & health checked access only available internally (via the service) by default
  (users can, of course, still access the kubernetes api by targeting individual control plane nodes)

## Who is this for?

People and organizations who create and manage multiple clusters using *GitOps* (e.g. via *CAPI*),
where the management / parent cluster is pretty much the only user who accesses the k8s api externally,
e.g. for managing via *Argo CD* (*Hub-and-Spoke*) or to deploy *Argo CD* initially (*Decentralized GitOps*).

## Usage

### Installation

via Helm
```
name: cluster-local-lb
repository: "oci://ghcr.io/coreflow-dev/charts"
version: "<check version tag in charts/cluster-local-lb container image>"
```

via Kustomize
```
```

### CapiInternalLb
If there is a *CAPI* `Cluster` called `mycluster` in namespace `mynamespace` then e.g.

```
apiVersion: internallb.io.schaber/v1alpha1
kind: CapiInternalLb
metadata:
  name: mycluster-k8s-api
  namespace: mynamespace
spec:
  clusterRef:
    name: mycluster
```

Creates a service called `mycluster-k8s-api-lb` in namespace `mynamespace` that allows you to talk
to the k8s-api of `mycluster`.

## Development

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/cluster-local-lb:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/cluster-local-lb:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/cluster-local-lb:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/cluster-local-lb/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
kubebuilder edit --plugins=helm/v2-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
