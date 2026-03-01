package file

// batchOrphanDeleteDTO is the request body for DELETE /objects/orphans/batch.
type batchOrphanDeleteDTO struct {
	IDs []string `json:"ids"`
	All bool     `json:"all"`
}

// batchS3UploadDTO is the request body for POST /objects/s3/batch-upload.
type batchS3UploadDTO struct {
	URLs []string `json:"urls"`
}
