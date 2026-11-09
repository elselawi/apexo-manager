package store

// Admin is a dashboard administrator. Password hashes are never serialized.
type Admin struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"displayName"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	Disabled     bool   `json:"disabled"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	LastLoginAt  string `json:"lastLoginAt"`
}

// Session is an authenticated dashboard session. Token hashes and CSRF tokens
// are never serialized to clients.
type Session struct {
	ID         string `json:"id"`
	AdminID    string `json:"-"`
	TokenHash  string `json:"-"`
	CSRFToken  string `json:"-"`
	CreatedAt  string `json:"createdAt"`
	ExpiresAt  string `json:"expiresAt"`
	LastSeenAt string `json:"lastSeenAt"`
	IP         string `json:"ip"`
	UserAgent  string `json:"userAgent"`
	RevokedAt  string `json:"revokedAt,omitempty"`
	Admin      *Admin `json:"admin,omitempty"`
	AdminName  string `json:"adminName,omitempty"`
	AdminUser  string `json:"adminUsername,omitempty"`
	AdminRole  string `json:"adminRole,omitempty"`
}

// Instance is a PocketBase installation tracked by the manager.
type Instance struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Hostname            string `json:"hostname"`
	BaseDir             string `json:"baseDir"`
	BinaryPath          string `json:"binaryPath"`
	DataDir             string `json:"dataDir"`
	Port                int    `json:"port"`
	ServiceName         string `json:"serviceName"`
	ServiceFile         string `json:"serviceFile"`
	RunAsUser           string `json:"runAsUser"`
	PBVersion           string `json:"pbVersion"`
	Status              string `json:"status"`
	Health              string `json:"health"`
	Managed             bool   `json:"managed"`
	Source              string `json:"source"`
	Notes               string `json:"notes"`
	DiscoveredJSON      string `json:"-"`
	CloudflareAccountID string `json:"cloudflareAccountId,omitempty"`
	SMTPProfileID       string `json:"smtpProfileId,omitempty"`
	StorageProfileID    string `json:"storageProfileId,omitempty"`
	CaddyMode           string `json:"caddyMode"`
	SMTPApplyStatus     string `json:"smtpApplyStatus"`
	SMTPAppliedAt       string `json:"smtpAppliedAt,omitempty"`
	StorageApplyStatus  string `json:"storageApplyStatus"`
	StorageAppliedAt    string `json:"storageAppliedAt,omitempty"`
	PBSuperuserEmail    string `json:"pbSuperuserEmail,omitempty"`
	PBSuperuserSecret   string `json:"-"`
	PBSuperuserManaged  bool   `json:"pbSuperuserManaged"`
	PBSuperuserRecordID string `json:"-"`
	PBLastVerifiedAt    string `json:"pbLastVerifiedAt,omitempty"`
	PBLastRepairedAt    string `json:"pbLastRepairedAt,omitempty"`
	PBReconcileError    string `json:"pbReconcileError,omitempty"`
	PBFailureCount      int    `json:"-"`
	PBAuthStatus        string `json:"pbAuthStatus"`
	PBAuthCheckedAt     string `json:"pbAuthCheckedAt,omitempty"`
	PBAuthError         string `json:"pbAuthError,omitempty"`
	BackupEnabled       bool   `json:"backupEnabled"`
	BackupCron          string `json:"backupCron"`
	BackupRetention     int    `json:"backupRetention"`
	NextBackupAt        string `json:"nextBackupAt,omitempty"`
	LastBackupAt        string `json:"lastBackupAt,omitempty"`
	LastHealthAt        string `json:"lastHealthAt,omitempty"`
	LastHealthError     string `json:"lastHealthError,omitempty"`
	LastSeenAt          string `json:"lastSeenAt,omitempty"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`

	// Transient, populated by list/get joins for convenience.
	SMTPProfileName    string   `json:"smtpProfileName,omitempty"`
	StorageProfileName string   `json:"storageProfileName,omitempty"`
	CloudflareName     string   `json:"cloudflareAccountName,omitempty"`
	Domains            []Domain `json:"domains,omitempty"`
	MemoryBytes        int64    `json:"memoryBytes,omitempty"`
	DiskBytes          int64    `json:"diskBytes,omitempty"`
	CPUPercent         float64  `json:"cpuPercent,omitempty"`
	PortListening      bool     `json:"portListening,omitempty"`
	LastBackupStatus   string   `json:"lastBackupStatus,omitempty"`
	LastBackupSize     int64    `json:"lastBackupSize,omitempty"`
	OpenJobID          string   `json:"openJobId,omitempty"`
}

// Domain maps a hostname to an instance and tracks DNS/Caddy/TLS state.
type Domain struct {
	ID                  string `json:"id"`
	InstanceID          string `json:"instanceId"`
	InstanceName        string `json:"instanceName,omitempty"`
	Hostname            string `json:"hostname"`
	CloudflareAccountID string `json:"cloudflareAccountId,omitempty"`
	ZoneID              string `json:"zoneId,omitempty"`
	ZoneName            string `json:"zoneName,omitempty"`
	DNSRecordID         string `json:"dnsRecordId,omitempty"`
	Proxied             bool   `json:"proxied"`
	DNSManaged          bool   `json:"dnsManaged"`
	CaddyManaged        bool   `json:"caddyManaged"`
	SnippetPath         string `json:"snippetPath,omitempty"`
	DNSStatus           string `json:"dnsStatus"`
	CaddyStatus         string `json:"caddyStatus"`
	TLSStatus           string `json:"tlsStatus"`
	TLSExpiresAt        string `json:"tlsExpiresAt,omitempty"`
	LastCheckedAt       string `json:"lastCheckedAt,omitempty"`
	LastError           string `json:"lastError,omitempty"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

// SMTPProfile stores reusable mail settings. Password is encrypted at rest and
// never exposed by the API.
type SMTPProfile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Password     string `json:"-"`
	HasPassword  bool   `json:"hasPassword"`
	AuthMethod   string `json:"authMethod"`
	TLSMode      string `json:"tlsMode"`
	LocalName    string `json:"localName"`
	FromAddress  string `json:"fromAddress"`
	FromName     string `json:"fromName"`
	Status       string `json:"status"`
	LastTestedAt string `json:"lastTestedAt,omitempty"`
	LastError    string `json:"lastError,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	UsageCount   int    `json:"usageCount,omitempty"`
}

// StorageProfile stores reusable S3-compatible settings. SecretKey is encrypted
// at rest and never exposed by the API.
type StorageProfile struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Provider       string `json:"provider"`
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	AccessKey      string `json:"accessKey"`
	SecretKey      string `json:"-"`
	HasSecret      bool   `json:"hasSecret"`
	ForcePathStyle bool   `json:"forcePathStyle"`
	Status         string `json:"status"`
	LastTestedAt   string `json:"lastTestedAt,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	UsageCount     int    `json:"usageCount,omitempty"`
}

// CloudflareAccount stores an encrypted API token.
type CloudflareAccount struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	APIToken        string `json:"-"`
	HasToken        bool   `json:"hasToken"`
	AccountID       string `json:"accountId,omitempty"`
	Status          string `json:"status"`
	LastValidatedAt string `json:"lastValidatedAt,omitempty"`
	LastError       string `json:"lastError,omitempty"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// Backup is a recorded backup artifact.
type Backup struct {
	ID           string `json:"id"`
	InstanceID   string `json:"instanceId"`
	InstanceName string `json:"instanceName,omitempty"`
	JobID        string `json:"jobId,omitempty"`
	Method       string `json:"method"`
	Filename     string `json:"filename"`
	Path         string `json:"-"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	Note         string `json:"note,omitempty"`
	Automatic    bool   `json:"automatic"`
	VerifiedAt   string `json:"verifiedAt,omitempty"`
	RestoredAt   string `json:"restoredAt,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

// Job is a durable background operation.
type Job struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Payload        string    `json:"payload"`
	Status         string    `json:"status"`
	Priority       int       `json:"priority"`
	Attempts       int       `json:"attempts"`
	MaxAttempts    int       `json:"maxAttempts"`
	AvailableAt    string    `json:"availableAt"`
	StartedAt      string    `json:"startedAt,omitempty"`
	FinishedAt     string    `json:"finishedAt,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
	Progress       int       `json:"progress"`
	Result         string    `json:"result,omitempty"`
	IdempotencyKey string    `json:"-"`
	CreatedBy      string    `json:"createdBy"`
	Canceled       bool      `json:"canceled"`
	RecoveredCount int       `json:"recoveredCount"`
	CreatedAt      string    `json:"createdAt"`
	UpdatedAt      string    `json:"updatedAt"`
	Steps          []JobStep `json:"steps,omitempty"`
}

// JobStep is a durable checkpoint within a job.
type JobStep struct {
	ID         string `json:"id"`
	JobID      string `json:"jobId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	Output     string `json:"output,omitempty"`
	Attempt    int    `json:"attempt"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

// AuditEntry is an append-only record of administrative activity.
type AuditEntry struct {
	ID         string `json:"id"`
	ActorType  string `json:"actorType"`
	ActorID    string `json:"actorId,omitempty"`
	ActorName  string `json:"actorName"`
	Action     string `json:"action"`
	TargetType string `json:"targetType,omitempty"`
	TargetID   string `json:"targetId,omitempty"`
	Detail     string `json:"detail,omitempty"`
	IP         string `json:"ip,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
	CreatedAt  string `json:"createdAt"`
}

// ServerMetrics is a sampled server health point.
type ServerMetrics struct {
	ID            int64   `json:"id"`
	CapturedAt    string  `json:"capturedAt"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemUsed       int64   `json:"memUsed"`
	MemTotal      int64   `json:"memTotal"`
	DiskUsed      int64   `json:"diskUsed"`
	DiskTotal     int64   `json:"diskTotal"`
	Load1         float64 `json:"load1"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
}

// InstanceMetrics is a sampled instance health point.
type InstanceMetrics struct {
	ID            int64   `json:"id"`
	InstanceID    string  `json:"instanceId"`
	CapturedAt    string  `json:"capturedAt"`
	Status        string  `json:"status"`
	Health        string  `json:"health"`
	MemBytes      int64   `json:"memBytes"`
	DiskBytes     int64   `json:"diskBytes"`
	CPUPercent    float64 `json:"cpuPercent"`
	PortListening bool    `json:"portListening"`
	Error         string  `json:"error,omitempty"`
}

// Page is a generic list result.
type Page[T any] struct {
	Items   []T `json:"items"`
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"perPage"`
}
