package example

const (
	DB_MYSQL_MODEL_BASED     = "db-mysql-model-based"
	DB_MYSQL_DISCOVERY_BASED = "db-mysql-discovery-based"
	DB_FILE                  = "db-file"
	DB_CACHE_REDIS           = "db-cache-redis"
	DB_INTERNAL              = "internal"
	DB_EXTERNAL              = "external"
)

var GrasTemplate = []string{
	DB_MYSQL_MODEL_BASED,
	DB_MYSQL_DISCOVERY_BASED,
	DB_FILE,
	DB_CACHE_REDIS,
}

var GrasDBType = []string{
	DB_INTERNAL,
	DB_EXTERNAL,
}

var (
	DeploymentNamespace string
	GrasName            string
)
