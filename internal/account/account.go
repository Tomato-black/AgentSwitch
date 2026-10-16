package account

type Account struct {
	ID, ProviderID, Name     string
	SecretKey, SecretBackend string
	Priority                 int
	Enabled                  bool
}
