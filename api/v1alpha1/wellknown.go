package v1alpha1

const (
	ClassLabel                 = "namespaceclass.akuity.io/name"              // on Namespaces
	ManagedByClassLabel        = "namespaceclass.akuity.io/class"             // on created objects
	ManagedResourcesAnnotation = "namespaceclass.akuity.io/managed-resources" // on Namespaces
)

// NamespaceClassKind is the kind in the owner reference of each created object.
const NamespaceClassKind = "NamespaceClass"
