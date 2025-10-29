package cloud

type ClusterInfo struct {
	Name           string
	Type           string
	Provider       string
	KubeconfigPath string
	Endpoint       string
	Status         string
}

type ClusterManager interface {
	CreateCluster(name, clusterType string) (*ClusterInfo, error)
	DeleteCluster(name string) error
	GetCluster(name string) (*ClusterInfo, error)
}