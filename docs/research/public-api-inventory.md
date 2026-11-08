# Public API inventory

DEV-101 requires a complete public API. The
[OpenAPI 3.1 operation object](https://spec.openapis.org/oas/v3.1.1.html#operation-object)
uses operation IDs to identify operations for tools. The
[paths rules](https://spec.openapis.org/oas/v3.1.1.html#paths-object)
treat parameter-name variants of the same path shape as identical.
[Kubernetes API discovery](https://kubernetes.io/docs/concepts/overview/kubernetes-api/#discovery-api)
separates the operation inventory from the schemas clients use.

Inventory the local versioned contracts with the SDK's audience rules. Emit
the domain, audience, method, path and operation ID. Report missing public
operation IDs and competing declarations of the same method and path shape
in an audience. A path and method can legitimately serve different audiences.
Check duplicate IDs within each OpenAPI document, where the specification
requires uniqueness. Report summary and description presence as measurements,
since OpenAPI allows them to be omitted. A machine-readable report and a
nonzero exit on findings support a repeatable release check.

Runtime discovery would need an authoritative route registry covering all
entrances. Inferring coverage from handler source names or by hitting each
route cannot distinguish all permission refusals, service paths and disabled
features. This tool audits contract declarations. Completion still requires
the owning modules' authorization and end-to-end evidence, documented
operations and agreement with the running dispatchers.

Use the existing SDK projection and the Python standard library. The
[PSF license](https://docs.python.org/3/license.html) permits offline and
commercial use and redistribution under its terms. No dependency, model,
dataset or remote lookup is added.

Test private and unlisted exclusion, multi-audience collisions, parameter
renaming, per-document ID collisions, and missing IDs. Run the report on the
current repository in a network namespace with no external routes. In the
current tree the cloud inventory contract contains six public operations
without IDs. The components and nodes contracts both declare four
node-exporter routes. The owning contracts and generated clients require
a coordinated change to resolve those records without breaking callers.
