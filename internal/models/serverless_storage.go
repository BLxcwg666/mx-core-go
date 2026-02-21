package models

// ServerlessStorageModel stores key-value data for serverless runtime.
type ServerlessStorageModel struct {
	Base
	Namespace string `json:"namespace" gorm:"not null;size:191;index:idx_serverless_storage_ns_key,unique"`
	Key       string `json:"key"       gorm:"not null;size:191;index:idx_serverless_storage_ns_key,unique"`
	Value     string `json:"value"     gorm:"type:longtext"`
}

func (ServerlessStorageModel) TableName() string { return "serverless_storages" }
