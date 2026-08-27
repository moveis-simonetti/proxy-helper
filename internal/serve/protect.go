package serve

// unprotect turns a password blob back into the plaintext. It is a package
// variable rather than a direct call so tests can stand in for the OS
// crypto, which is not reachable on the platform this project is developed
// and tested on.
var unprotect = unprotectPassword
