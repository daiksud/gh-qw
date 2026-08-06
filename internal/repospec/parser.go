package repospec

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"
)

const defaultHost = "github.com"

// ErrUsage identifies repository specifications that should be reported as
// command-line usage errors.
var ErrUsage = errors.New("invalid repository specification")

// UsageError describes an invalid repository argument.
type UsageError struct {
	Input  string
	Reason string
	Err    error
}

func (e *UsageError) Error() string {
	reason := e.Reason
	if reason == "" && e.Err != nil {
		reason = e.Err.Error()
	}
	if reason == "" {
		reason = ErrUsage.Error()
	}
	return fmt.Sprintf("invalid repository specification %q: %s", e.Input, reason)
}

// Unwrap preserves resolver and filesystem errors for callers that need them.
func (e *UsageError) Unwrap() error {
	return e.Err
}

// Is makes every UsageError discoverable with errors.Is(err, ErrUsage).
func (e *UsageError) Is(target error) bool {
	return target == ErrUsage
}

// IdentityResolver returns the authenticated host and username used to expand
// a bare repository name. A caller may capture a context in this function.
type IdentityResolver func() (host, username string, err error)

// Options controls repository parsing.
type Options struct {
	SSH             bool
	Roots           []string
	WorkingDir      string
	ResolveIdentity IdentityResolver
	// RejectFileScheme rejects a file:// input (local or non-local
	// authority) as a usage error. get sets this because it clones through
	// `gh repo clone`, which never accepts a file:// URL; other callers such
	// as list and migrate leave it unset because they support file:// input.
	RejectFileScheme bool
}

// Spec is a normalized repository specification.
type Spec struct {
	CanonicalURL string
	CloneURL     string
	Identity     string
	Host         string
	Owner        string
	Repo         string
	Branch       string
}

type parser struct {
	input   string
	options Options
}

type authority struct {
	hostname string
	urlHost  string
	port     string
}

type repositoryPath struct {
	owner   string
	rawRepo string
	repo    string
	branch  string
}

type localRepositoryPath struct {
	repositoryPath
	host     string
	pathPart string
	root     string
}

// Parse converts a ghq-style repository argument into a normalized Spec.
func Parse(input string, options Options) (Spec, error) {
	p := parser{input: input, options: options}
	spec, err := p.parse()
	if err == nil {
		return spec, nil
	}

	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return Spec{}, err
	}
	return Spec{}, &UsageError{
		Input:  input,
		Reason: err.Error(),
		Err:    err,
	}
}

func (p parser) parse() (Spec, error) {
	if p.input == "" {
		return Spec{}, errors.New("repository is empty")
	}
	if strings.TrimSpace(p.input) != p.input {
		return Spec{}, errors.New("repository has leading or trailing whitespace")
	}
	for _, r := range p.input {
		if r == 0 {
			return Spec{}, errors.New("repository contains NUL")
		}
		if unicode.IsControl(r) {
			return Spec{}, errors.New("repository contains a control character")
		}
	}
	if strings.ContainsRune(p.input, '\\') {
		return Spec{}, errors.New("repository contains an unsupported path separator")
	}

	if strings.Contains(p.input, "://") {
		return p.parseURL()
	}
	if isRelativeForm(p.input) {
		return p.parseRelative()
	}
	if looksLikeSCPLike(p.input) {
		return p.parseSCPLike()
	}
	return p.parsePlain()
}

func (p parser) parseURL() (Spec, error) {
	u, err := url.Parse(p.input)
	if err != nil {
		return Spec{}, fmt.Errorf("parse URL: %w", err)
	}
	if u.Scheme == "" || u.Opaque != "" {
		return Spec{}, errors.New("URL must use an absolute hierarchical scheme")
	}
	if strings.ContainsAny(p.input, "?#") || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" {
		return Spec{}, errors.New("URL query and fragment are not supported")
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "file" && (u.RawPath != "" || u.EscapedPath() != u.Path) {
		return Spec{}, errors.New("escaped repository paths are not supported")
	}
	if strings.ContainsRune(u.Path, '\\') {
		return Spec{}, errors.New("URL path contains an unsupported path separator")
	}
	for _, r := range u.Path {
		if r == 0 || unicode.IsControl(r) {
			return Spec{}, errors.New("URL path contains NUL or a control character")
		}
	}

	switch scheme {
	case "http", "https", "ssh":
		return p.parseRemoteURL(u, scheme)
	case "file":
		if p.options.RejectFileScheme {
			return Spec{}, errors.New("file:// input is not supported")
		}
		return p.parseFileURL(u)
	default:
		return Spec{}, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
}

func (p parser) parseRemoteURL(u *url.URL, scheme string) (Spec, error) {
	if u.Host == "" {
		return Spec{}, errors.New("URL host is empty")
	}
	auth, err := parseAuthority(u.Host)
	if err != nil {
		return Spec{}, err
	}
	username, err := parseURLUser(u)
	if err != nil {
		return Spec{}, err
	}
	repository, err := parseRemoteRepositoryPath(u.Path)
	if err != nil {
		return Spec{}, err
	}

	cloneScheme := scheme
	cloneUser := username
	if p.options.SSH && (scheme == "http" || scheme == "https") {
		cloneScheme = "ssh"
		if cloneUser == "" {
			cloneUser = "git"
		}
	}
	cloneURL := buildRepositoryURL(cloneScheme, auth.urlHost, cloneUser, repository.owner, repository.rawRepo)
	return makeSpec(auth, repository, cloneURL), nil
}

func (p parser) parseFileURL(u *url.URL) (Spec, error) {
	if u.User != nil {
		return Spec{}, errors.New("file URL must not contain user information")
	}
	if u.Path == "" {
		return Spec{}, errors.New("file URL path is empty")
	}

	var fileHost string
	if u.Host != "" {
		auth, err := parseAuthority(u.Host)
		if err != nil {
			return Spec{}, err
		}
		if auth.port != "" {
			return Spec{}, errors.New("file URL must not contain a port")
		}
		fileHost = auth.hostname
		if auth.hostname != "localhost" {
			repository, err := parseRemoteRepositoryPath(u.Path)
			if err != nil {
				return Spec{}, err
			}
			cloneURL := buildRepositoryURL("file", auth.urlHost, "", repository.owner, repository.rawRepo)
			return makeSpec(auth, repository, cloneURL), nil
		}
	}

	localPath, err := p.parseLocalPath(u.Path, true)
	if err != nil {
		return Spec{}, err
	}
	auth := authority{hostname: localPath.host, urlHost: localPath.host}
	clonePath, err := cleanFileURLPath(localPath.pathPart)
	if err != nil {
		return Spec{}, err
	}
	clone := (&url.URL{Scheme: "file", Host: fileHost, Path: clonePath}).String()
	return makeSpec(auth, localPath.repositoryPath, clone), nil
}

func (p parser) parseRelative() (Spec, error) {
	localPath, err := p.parseLocalPath(p.input, false)
	if err != nil {
		return Spec{}, err
	}
	auth := authority{hostname: localPath.host, urlHost: localPath.host}
	cloneScheme := "https"
	cloneUser := ""
	if p.options.SSH {
		cloneScheme = "ssh"
		cloneUser = "git"
	}
	cloneURL := buildRepositoryURL(
		cloneScheme,
		auth.urlHost,
		cloneUser,
		localPath.owner,
		localPath.rawRepo,
	)
	return makeSpec(auth, localPath.repositoryPath, cloneURL), nil
}

func (p parser) parseSCPLike() (Spec, error) {
	colon := strings.IndexByte(p.input, ':')
	if colon <= 0 {
		return Spec{}, errors.New("malformed scp-like repository")
	}
	left, rawPath := p.input[:colon], p.input[colon+1:]
	if rawPath == "" {
		return Spec{}, errors.New("scp-like repository path is empty")
	}

	var username string
	host := left
	if at := strings.IndexByte(left, '@'); at >= 0 {
		if strings.IndexByte(left[at+1:], '@') >= 0 {
			return Spec{}, errors.New("scp-like authority contains multiple usernames")
		}
		username = left[:at]
		host = left[at+1:]
		if err := validateUsername(username); err != nil {
			return Spec{}, err
		}
	}
	auth, err := parseAuthority(host)
	if err != nil {
		return Spec{}, err
	}

	rawPath = strings.TrimPrefix(rawPath, "/")
	repository, err := parseRepositoryPath(rawPath)
	if err != nil {
		return Spec{}, err
	}
	cloneURL := buildRepositoryURL("ssh", auth.urlHost, username, repository.owner, repository.rawRepo)
	return makeSpec(auth, repository, cloneURL), nil
}

func (p parser) parsePlain() (Spec, error) {
	parts, branch, err := splitPlainRepository(p.input)
	if err != nil {
		return Spec{}, err
	}
	if branch != "" {
		if err := validateBranch(branch); err != nil {
			return Spec{}, err
		}
	}

	var auth authority
	var owner, rawRepo string
	switch len(parts) {
	case 1:
		rawRepo = parts[0]
		if p.options.ResolveIdentity == nil {
			return Spec{}, errors.New("a bare repository requires an authenticated identity; use <owner>/<repo>")
		}
		host, username, resolveErr := p.options.ResolveIdentity()
		if resolveErr != nil {
			return Spec{}, fmt.Errorf("resolve authenticated identity; use <owner>/<repo>: %w", resolveErr)
		}
		if host == "" || username == "" {
			return Spec{}, errors.New("authenticated identity is missing or ambiguous; use <owner>/<repo>")
		}
		auth, err = parseAuthority(host)
		if err != nil {
			return Spec{}, fmt.Errorf("authenticated host: %w", err)
		}
		owner = username
	case 2:
		auth, err = parseAuthority(defaultHost)
		if err != nil {
			return Spec{}, err
		}
		owner, rawRepo = parts[0], parts[1]
	case 3:
		auth, err = parseAuthority(parts[0])
		if err != nil {
			return Spec{}, err
		}
		owner, rawRepo = parts[1], parts[2]
	default:
		return Spec{}, errors.New("repository path must be <repo>, <owner>/<repo>, or <host>/<owner>/<repo>")
	}

	if err := validatePathComponent("owner", owner); err != nil {
		return Spec{}, err
	}
	repo, err := normalizeRepository(rawRepo)
	if err != nil {
		return Spec{}, err
	}
	repository := repositoryPath{
		owner:   owner,
		rawRepo: rawRepo,
		repo:    repo,
		branch:  branch,
	}

	cloneScheme := "https"
	cloneUser := ""
	if p.options.SSH {
		cloneScheme = "ssh"
		cloneUser = "git"
	}
	cloneURL := buildRepositoryURL(cloneScheme, auth.urlHost, cloneUser, owner, rawRepo)
	return makeSpec(auth, repository, cloneURL), nil
}

func (p parser) parseLocalPath(rawPath string, absolute bool) (localRepositoryPath, error) {
	if len(p.options.Roots) == 0 {
		return localRepositoryPath{}, errors.New("local repository forms require configured roots")
	}

	workingDir := p.options.WorkingDir
	if !absolute && workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return localRepositoryPath{}, fmt.Errorf("get working directory: %w", err)
		}
	}

	var candidates []localRepositoryPath
	var branchErr error
	for offset := 0; ; {
		relative := strings.IndexByte(rawPath[offset:], '@')
		if relative < 0 {
			break
		}
		index := offset + relative
		pathPart := rawPath[:index]
		match, err := matchLocalRepository(pathPart, workingDir, p.options.Roots, absolute)
		if err == nil {
			branch := rawPath[index+1:]
			if err := validateBranch(branch); err != nil {
				if branchErr == nil {
					branchErr = err
				}
			} else {
				match.branch = branch
				match.pathPart = pathPart
				candidates = append(candidates, match)
			}
		}
		offset = index + 1
	}

	plain, plainErr := matchLocalRepository(rawPath, workingDir, p.options.Roots, absolute)
	if plainErr == nil {
		plain.pathPart = rawPath
		candidates = append(candidates, plain)
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		first := candidates[0]
		for _, candidate := range candidates[1:] {
			if candidate.host != first.host ||
				candidate.owner != first.owner ||
				candidate.repo != first.repo ||
				candidate.branch != first.branch ||
				candidate.pathPart != first.pathPart {
				return localRepositoryPath{}, errors.New("local repository and branch suffix are ambiguous")
			}
		}
		return first, nil
	}
	if branchErr != nil {
		return localRepositoryPath{}, branchErr
	}
	if plainErr != nil {
		return localRepositoryPath{}, plainErr
	}
	return localRepositoryPath{}, errors.New("local path does not identify a repository")
}

func matchLocalRepository(rawPath, workingDir string, roots []string, absolute bool) (localRepositoryPath, error) {
	if rawPath == "" {
		return localRepositoryPath{}, errors.New("local repository path is empty")
	}
	if err := validateLocalPathSyntax(rawPath, absolute); err != nil {
		return localRepositoryPath{}, err
	}

	var candidate string
	if absolute {
		var err error
		candidate, err = fileURLToPath(rawPath)
		if err != nil {
			return localRepositoryPath{}, err
		}
		if !filepath.IsAbs(candidate) {
			return localRepositoryPath{}, errors.New("file URL path must be absolute")
		}
	} else {
		if !isRelativeForm(rawPath) {
			return localRepositoryPath{}, errors.New("local repository path must use ./ or ../")
		}
		candidate = filepath.FromSlash(rawPath)
		if filepath.IsAbs(candidate) {
			return localRepositoryPath{}, errors.New("relative repository path must not be absolute")
		}
		candidate = filepath.Join(workingDir, candidate)
	}

	candidate, err := canonicalFilesystemPath(candidate)
	if err != nil {
		return localRepositoryPath{}, fmt.Errorf("resolve local repository path: %w", err)
	}

	var matches []localRepositoryPath
	for _, configuredRoot := range roots {
		if configuredRoot == "" {
			continue
		}
		root, err := canonicalFilesystemPath(configuredRoot)
		if err != nil {
			return localRepositoryPath{}, fmt.Errorf("resolve repository root %q: %w", configuredRoot, err)
		}
		relative, err := filepath.Rel(root, candidate)
		if err != nil || !pathIsWithinRoot(relative) {
			continue
		}
		components := strings.Split(filepath.ToSlash(relative), "/")
		if len(components) != 3 {
			continue
		}

		auth, err := parseAuthority(components[0])
		if err != nil || auth.port != "" {
			continue
		}
		if err := validatePathComponent("owner", components[1]); err != nil {
			continue
		}
		repo, err := normalizeRepository(components[2])
		if err != nil {
			continue
		}
		matches = append(matches, localRepositoryPath{
			repositoryPath: repositoryPath{
				owner:   components[1],
				rawRepo: components[2],
				repo:    repo,
			},
			host: auth.hostname,
			root: root,
		})
	}

	if len(matches) == 0 {
		return localRepositoryPath{}, errors.New("local path is not a <host>/<owner>/<repo> directory under a configured root")
	}
	best := matches[0]
	for _, match := range matches[1:] {
		if match.host != best.host || match.owner != best.owner || match.repo != best.repo {
			return localRepositoryPath{}, errors.New("local path maps to multiple repository identities")
		}
		if len(match.root) > len(best.root) {
			best = match
		}
	}
	return best, nil
}

func parseRemoteRepositoryPath(path string) (repositoryPath, error) {
	if !strings.HasPrefix(path, "/") {
		return repositoryPath{}, errors.New("URL repository path must be absolute")
	}
	if strings.HasPrefix(path, "//") {
		return repositoryPath{}, errors.New("URL repository path contains an empty component")
	}
	return parseRepositoryPath(strings.TrimPrefix(path, "/"))
}

func parseRepositoryPath(path string) (repositoryPath, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return repositoryPath{}, errors.New("repository path is missing owner or repository")
	}

	owner := parts[0]
	repoPart := parts[1]
	branch := ""
	if at := strings.IndexByte(repoPart, '@'); at >= 0 {
		branchParts := append([]string{repoPart[at+1:]}, parts[2:]...)
		branch = strings.Join(branchParts, "/")
		repoPart = repoPart[:at]
	} else if len(parts) != 2 {
		return repositoryPath{}, errors.New("repository path must contain exactly owner and repository")
	}

	if err := validatePathComponent("owner", owner); err != nil {
		return repositoryPath{}, err
	}
	repo, err := normalizeRepository(repoPart)
	if err != nil {
		return repositoryPath{}, err
	}
	if branch != "" {
		if err := validateBranch(branch); err != nil {
			return repositoryPath{}, err
		}
	} else if strings.Contains(path, "@") {
		return repositoryPath{}, errors.New("branch suffix is empty")
	}

	return repositoryPath{
		owner:   owner,
		rawRepo: repoPart,
		repo:    repo,
		branch:  branch,
	}, nil
}

func splitPlainRepository(input string) ([]string, string, error) {
	parts := strings.Split(input, "/")
	for index, part := range parts {
		at := strings.IndexByte(part, '@')
		if at < 0 {
			continue
		}
		repositoryParts := append([]string(nil), parts[:index+1]...)
		repositoryParts[index] = part[:at]
		branchParts := append([]string{part[at+1:]}, parts[index+1:]...)
		branch := strings.Join(branchParts, "/")
		if branch == "" {
			return nil, "", errors.New("branch suffix is empty")
		}
		if len(repositoryParts) > 3 {
			return nil, "", errors.New("repository path has too many components before the branch suffix")
		}
		return repositoryParts, branch, nil
	}
	if len(parts) > 3 {
		return nil, "", errors.New("repository path must be <repo>, <owner>/<repo>, or <host>/<owner>/<repo>")
	}
	return parts, "", nil
}

func parseURLUser(u *url.URL) (string, error) {
	if u.User == nil {
		return "", nil
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return "", errors.New("URL passwords are not supported")
	}
	username := u.User.Username()
	if err := validateUsername(username); err != nil {
		return "", err
	}
	return username, nil
}

func parseAuthority(raw string) (authority, error) {
	if raw == "" {
		return authority{}, errors.New("host is empty")
	}
	if strings.ContainsAny(raw, "/\\@") {
		return authority{}, errors.New("host contains an invalid separator or user")
	}
	if strings.HasPrefix(raw, "[") || strings.HasSuffix(raw, "]") || strings.Count(raw, ":") > 1 {
		return authority{}, errors.New("IPv6 hosts are not supported")
	}

	host := raw
	port := ""
	if colon := strings.LastIndexByte(raw, ':'); colon >= 0 {
		host, port = raw[:colon], raw[colon+1:]
		if host == "" || port == "" {
			return authority{}, errors.New("host or port is empty")
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return authority{}, fmt.Errorf("invalid port %q", port)
		}
		port = strconv.Itoa(portNumber)
	}

	host = strings.ToLower(host)
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return authority{}, errors.New("IPv6 hosts are not supported")
		}
		host = ip.String()
	} else if err := validateDNSHost(host); err != nil {
		return authority{}, err
	}

	urlHost := host
	if port != "" {
		urlHost += ":" + port
	}
	return authority{hostname: host, urlHost: urlHost, port: port}, nil
}

func validateDNSHost(host string) error {
	if host == "" {
		return errors.New("host is empty")
	}
	if len(host) > 253 || strings.HasSuffix(host, ".") {
		return fmt.Errorf("invalid host %q", host)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("invalid host %q", host)
		}
		for index, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				continue
			}
			if r == '-' && index > 0 && index < len(label)-1 {
				continue
			}
			return fmt.Errorf("invalid host %q", host)
		}
	}
	return nil
}

func normalizeRepository(raw string) (string, error) {
	repo := strings.TrimSuffix(raw, ".git")
	if err := validatePathComponent("repository", repo); err != nil {
		return "", err
	}
	return repo, nil
}

func validatePathComponent(kind, component string) error {
	if component == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if component == "." || component == ".." {
		return fmt.Errorf("%s must not be %q", kind, component)
	}
	if strings.HasSuffix(component, ".") {
		return fmt.Errorf("%s must not end with a dot", kind)
	}
	for _, r := range component {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' ||
			r == '_' ||
			r == '-' {
			continue
		}
		return fmt.Errorf("%s contains unsafe character %q", kind, r)
	}
	return nil
}

func validateUsername(username string) error {
	if username == "" {
		return errors.New("URL username is empty")
	}
	for _, r := range username {
		if r == 0 || unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("URL username contains whitespace or a control character")
		}
		switch r {
		case '/', '\\', ':', '@', '?', '#':
			return fmt.Errorf("URL username contains unsafe character %q", r)
		}
	}
	return nil
}

func validateBranch(branch string) error {
	if branch == "" {
		return errors.New("branch suffix is empty")
	}
	if branch == "@" {
		return errors.New("branch must not be @")
	}
	if strings.HasPrefix(branch, "-") {
		return errors.New("branch must not start with a dash")
	}
	if strings.Contains(branch, "..") {
		return errors.New("branch must not contain ..")
	}
	if strings.Contains(branch, "@{") {
		return errors.New("branch must not contain @{")
	}
	for _, r := range branch {
		if r == 0 || unicode.IsControl(r) || unicode.IsSpace(r) {
			return errors.New("branch contains whitespace or a control character")
		}
		switch r {
		case '\\', '~', '^', ':', '?', '*', '[':
			return fmt.Errorf("branch contains invalid character %q", r)
		}
	}
	for _, component := range strings.Split(branch, "/") {
		if component == "" {
			return errors.New("branch contains an empty component")
		}
		if component == "." || component == ".." {
			return fmt.Errorf("branch contains invalid component %q", component)
		}
		if strings.HasPrefix(component, ".") {
			return fmt.Errorf("branch component %q starts with a dot", component)
		}
		if strings.HasSuffix(component, ".") || strings.HasSuffix(component, ".lock") {
			return fmt.Errorf("branch component %q has an invalid suffix", component)
		}
	}
	return nil
}

func makeSpec(auth authority, repository repositoryPath, cloneURL string) Spec {
	identity := strings.Join([]string{auth.hostname, repository.owner, repository.repo}, "/")
	return Spec{
		CanonicalURL: buildRepositoryURL("https", auth.urlHost, "", repository.owner, repository.repo),
		CloneURL:     cloneURL,
		Identity:     identity,
		Host:         auth.hostname,
		Owner:        repository.owner,
		Repo:         repository.repo,
		Branch:       repository.branch,
	}
}

func buildRepositoryURL(scheme, host, username, owner, repo string) string {
	u := &url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   "/" + owner + "/" + repo,
	}
	if username != "" {
		u.User = url.User(username)
	}
	return u.String()
}

func looksLikeSCPLike(input string) bool {
	colon := strings.IndexByte(input, ':')
	if colon <= 0 {
		return false
	}
	slash := strings.IndexByte(input, '/')
	if slash >= 0 && slash < colon {
		return false
	}
	if colon == 1 {
		first := input[0]
		if (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') {
			return false
		}
	}
	if slash >= 0 {
		portCandidate := input[colon+1 : slash]
		if portCandidate != "" && allDecimal(portCandidate) && !strings.Contains(input[:colon], "@") {
			return false
		}
	}
	return true
}

func allDecimal(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func isRelativeForm(input string) bool {
	return input == "." ||
		input == ".." ||
		strings.HasPrefix(input, "./") ||
		strings.HasPrefix(input, "../")
}

func validateLocalPathSyntax(path string, absolute bool) error {
	components := strings.Split(path, "/")
	if absolute {
		if len(components) < 2 || components[0] != "" {
			return errors.New("file URL path must be absolute")
		}
		components = components[1:]
		for _, component := range components {
			if component == "" {
				return errors.New("file URL path contains an empty component")
			}
			if component == "." || component == ".." {
				return fmt.Errorf("file URL path contains invalid component %q", component)
			}
		}
		return nil
	}

	seenRepositoryComponent := false
	for _, component := range components {
		if component == "" {
			return errors.New("relative repository path contains an empty component")
		}
		if component == "." || component == ".." {
			if seenRepositoryComponent {
				return fmt.Errorf("relative repository path contains invalid component %q", component)
			}
			continue
		}
		seenRepositoryComponent = true
	}
	return nil
}

func pathIsWithinRoot(relative string) bool {
	return relative != "." &&
		relative != ".." &&
		!filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func canonicalFilesystemPath(path string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}

	current := absolute
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func fileURLToPath(urlPath string) (string, error) {
	if urlPath == "" {
		return "", errors.New("file URL path is empty")
	}
	path := filepath.FromSlash(urlPath)
	if runtime.GOOS == "windows" &&
		len(path) >= 3 &&
		path[0] == filepath.Separator &&
		((path[1] >= 'a' && path[1] <= 'z') || (path[1] >= 'A' && path[1] <= 'Z')) &&
		path[2] == ':' {
		path = path[1:]
	}
	return path, nil
}

func cleanFileURLPath(urlPath string) (string, error) {
	path, err := fileURLToPath(urlPath)
	if err != nil {
		return "", err
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", errors.New("file URL path must be absolute")
	}
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path, nil
}
