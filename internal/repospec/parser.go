package repospec

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

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

// Input is repository syntax that has not been resolved against the
// environment. Parse never reads the filesystem, runs Git, or authenticates.
type Input struct {
	raw        string
	authority  authority
	repository repositoryPath
	scheme     string
	username   string
	localPath  string
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

// Parse checks repository-input syntax while preserving literal transport
// and branch information. Resolve performs any required environment access.
func Parse(input string) (Input, error) {
	parsed, err := (Input{raw: input}).parse()
	if err != nil {
		return Input{}, inputError(input, err)
	}
	return parsed, nil
}

func inputError(input string, err error) error {
	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return err
	}
	return &UsageError{Input: input, Reason: err.Error(), Err: err}
}

func (p Input) parse() (Input, error) {
	if p.raw == "" {
		return Input{}, errors.New("repository is empty")
	}
	if strings.TrimSpace(p.raw) != p.raw {
		return Input{}, errors.New("repository has leading or trailing whitespace")
	}
	for _, r := range p.raw {
		if r == 0 {
			return Input{}, errors.New("repository contains NUL")
		}
		if unicode.IsControl(r) {
			return Input{}, errors.New("repository contains a control character")
		}
	}
	if strings.ContainsRune(p.raw, '\\') {
		return Input{}, errors.New("repository contains an unsupported path separator")
	}
	if strings.Contains(p.raw, "://") {
		return p.parseURL()
	}
	if isRelativeForm(p.raw) {
		p.localPath = p.raw
		return p, nil
	}
	if looksLikeSCPLike(p.raw) {
		return p.parseSCPLike()
	}
	return p.parsePlain()
}

func (p Input) parseURL() (Input, error) {
	u, err := url.Parse(p.raw)
	if err != nil {
		return Input{}, fmt.Errorf("parse URL: %w", err)
	}
	if u.Scheme == "" || u.Opaque != "" {
		return Input{}, errors.New("URL must use an absolute hierarchical scheme")
	}
	if strings.ContainsAny(p.raw, "?#") || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.RawFragment != "" {
		return Input{}, errors.New("URL query and fragment are not supported")
	}
	p.scheme = strings.ToLower(u.Scheme)
	if p.scheme != "file" && (u.RawPath != "" || u.EscapedPath() != u.Path) {
		return Input{}, errors.New("escaped repository paths are not supported")
	}
	if strings.ContainsRune(u.Path, '\\') {
		return Input{}, errors.New("URL path contains an unsupported path separator")
	}
	for _, r := range u.Path {
		if r == 0 || unicode.IsControl(r) {
			return Input{}, errors.New("URL path contains NUL or a control character")
		}
	}
	switch p.scheme {
	case "http", "https", "ssh":
		p.authority, err = parseAuthority(u.Host)
		if err != nil {
			return Input{}, err
		}
		p.username, err = parseURLUser(u)
		if err != nil {
			return Input{}, err
		}
	case "file":
		if u.User != nil {
			return Input{}, errors.New("file URL must not contain user information")
		}
		if u.Path == "" {
			return Input{}, errors.New("file URL path is empty")
		}
		if u.Host != "" {
			p.authority, err = parseAuthority(u.Host)
			if err != nil {
				return Input{}, err
			}
			if p.authority.port != "" {
				return Input{}, errors.New("file URL must not contain a port")
			}
		}
		if u.Host == "" || p.authority.hostname == "localhost" {
			p.localPath = u.Path
			return p, nil
		}
	default:
		return Input{}, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	p.repository, err = parseRemoteRepositoryPath(u.Path)
	if err != nil {
		return Input{}, err
	}
	return p, nil
}

func (p Input) parseSCPLike() (Input, error) {
	colon := strings.IndexByte(p.raw, ':')
	if colon <= 0 {
		return Input{}, errors.New("malformed scp-like repository")
	}
	left, rawPath := p.raw[:colon], p.raw[colon+1:]
	if rawPath == "" {
		return Input{}, errors.New("scp-like repository path is empty")
	}
	host := left
	if at := strings.IndexByte(left, '@'); at >= 0 {
		if strings.IndexByte(left[at+1:], '@') >= 0 {
			return Input{}, errors.New("scp-like authority contains multiple usernames")
		}
		p.username = left[:at]
		host = left[at+1:]
		if err := validateUsername(p.username); err != nil {
			return Input{}, err
		}
	}
	var err error
	p.authority, err = parseAuthority(host)
	if err != nil {
		return Input{}, err
	}
	p.repository, err = parseRepositoryPath(strings.TrimPrefix(rawPath, "/"))
	if err != nil {
		return Input{}, err
	}
	p.scheme = "ssh"
	return p, nil
}

func (p Input) parsePlain() (Input, error) {
	parts, branch, err := splitPlainRepository(p.raw)
	if err != nil {
		return Input{}, err
	}
	var owner, rawRepo string
	switch len(parts) {
	case 1:
		rawRepo = parts[0]
	case 2:
		owner, rawRepo = parts[0], parts[1]
	case 3:
		p.authority, err = parseAuthority(parts[0])
		if err != nil {
			return Input{}, err
		}
		owner, rawRepo = parts[1], parts[2]
	default:
		return Input{}, errors.New("repository path must be <repo>, <owner>/<repo>, or <host>/<owner>/<repo>")
	}
	if len(parts) > 1 {
		if err := validatePathComponent("owner", owner); err != nil {
			return Input{}, err
		}
	}
	repo, err := normalizeRepository(rawRepo)
	if err != nil {
		return Input{}, err
	}
	p.repository = repositoryPath{owner: owner, rawRepo: rawRepo, repo: repo, branch: branch}
	return p, nil
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
	if branch == "" && strings.Contains(path, "@") {
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
