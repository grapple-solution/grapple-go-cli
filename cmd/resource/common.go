package resource

// Global flag variables (which you may bind in init())
var (
	GRASName         string
	GRASTemplate     string
	DBType           string
	ModelsInput      string
	RelationsInput   string
	DatasourcesInput string
	DiscoveriesInput string
	DatabaseSchema   string
	AutoDiscovery    bool
	SourceData       string
	EnableGRUIM      bool
	DBFilePath       string
	KubeContext      string
	KubeNS           string

	// Constants (adjust as needed)
	awsRegistry                = "p7h7z5g3"
	templateFileDest           = "/tmp/template.yaml" // working template file location
	kubeblocksTemplateFileDest = "/tmp/kube_db.yaml"

	// Additional Global variables
	URL string
)
