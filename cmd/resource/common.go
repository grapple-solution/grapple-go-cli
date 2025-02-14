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
	DB_FILE                  = "db-file"
	DB_MYSQL_MODEL_BASED     = "db-mysql-model-based"
	DB_MYSQL_DISCOVERY_BASED = "db-mysql-discovery-based"
	INTERNAL_DB              = "internal"
	EXTERNAL_DB              = "external"
	awsRegistry              = "p7h7z5g3"
	templateFileDest         = "/tmp/template.yaml" // working template file location
	tmpFileDest              = "/tmp/temp_file"
)
