package utils

const (
	DB_MYSQL_MODEL_BASED     = "db-mysql-model-based"
	DB_MYSQL_DISCOVERY_BASED = "db-mysql-discovery-based"
	DB_FILE                  = "db-file"
	DB_CACHE_REDIS           = "db-cache-redis"
	DB_INTERNAL              = "internal"
	DB_EXTERNAL              = "external"
)

var GrasTemplates = []string{
	DB_MYSQL_MODEL_BASED,
	DB_MYSQL_DISCOVERY_BASED,
	DB_FILE,
	DB_CACHE_REDIS,
}

var GrasDBType = []string{
	DB_INTERNAL,
	DB_EXTERNAL,
}

// ANSI color codes
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
)

// Regex constants
const (
	NonEmptyValueRegex                    = "^.+$"
	EmptyValueRegex                       = ".*"
	EmailRegex                            = "^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}$"
	AlphaNumericWithHyphenUnderscoreRegex = "^[a-zA-Z0-9_-]+$"
)

// Default values
const (
	DefaultValue = ""
)

const (
	SecKeyEmail               = "email"
	SecKeyOrganization        = "organization"
	SecKeyClusterdomain       = "clusterdomain"
	SecKeyGrapiversion        = "grapiversion"
	SecKeyGruimversion        = "gruimversion"
	SecKeyDev                 = "dev"
	SecKeySsl                 = "ssl"
	SecKeySslissuer           = "sslissuer"
	SecKeyClusterName         = "CLUSTER_NAME"
	SecKeyGrapleDNS           = "GRAPPLE_DNS"
	SecKeyGrapleVersion       = "GRAPPLE_VERSION"
	SecKeyGrapleLicense       = "GRAPPLE_LICENSE"
	SecKeyProviderClusterType = "PROVIDER_CLUSTER_TYPE"
	SecKeyCivoClusterID       = "CIVO_CLUSTER_ID"
	SecKeyCivoRegion          = "CIVO_REGION"
	SecKeyCivoMasterIP        = "CIVO_MASTER_IP"
)

const (
	ProviderClusterTypeCivo = "CIVO"
	ProviderClusterTypeK3d  = "K3D"
)
