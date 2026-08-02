# Security Scan — Layer Reference

Loaded by the bundled `security-scan` skill. This reviewer-only copy contains
the detection patterns needed by the six scan layers without delegating to
unbundled agents or skills.

## Layer 1: Package vulnerabilities

Run `dotnet list package --vulnerable --include-transitive`. Treat CVSS 9–10 as
critical, 7–8.9 as high, 4–6.9 as medium, and lower non-zero scores as low.
Remediate with a patched version or document compensating controls.

## Layer 2: Secrets detection

Inspect C#, JSON, YAML, XML, and configuration files for private keys, bearer
tokens, cloud keys, connection-string passwords, and literal values assigned
to API-key/secret/token variables. Exclude obvious placeholders, fake test
fixtures, development-only values, and `UserSecretsId` declarations.

## Layer 3: OWASP code patterns

- Injection: concatenated/raw SQL with user input and unencoded raw HTML.
- Integrity/deserialization: `BinaryFormatter` or unsafe type-name handling.
- Cryptography: MD5/SHA1 for security, ECB, hardcoded keys, or weak password
  derivation.
- Access control: resource IDs used without an ownership/authorization check.

## Layer 4: Authentication and authorization

Verify explicit public/protected endpoint posture, strict issuer/audience/
lifetime/signing-key validation, narrowly scoped policies, correct middleware
ordering, and the absence of authorization bypasses.

## Layer 5: CORS

Flag wildcard origins, especially with credentials, and overly broad methods or
headers. Prefer explicit configuration-backed origins, methods, and headers and
verify development/production policy separation.

## Layer 6: Data protection

Check for PII in logs, over-broad response entities, plaintext tokens/API keys,
and production secrets in tracked configuration. Prefer identifiers in logs,
response DTOs, platform data protection, and external secret providers.

## Findings

Every finding includes severity, `file:line`, OWASP category, impact, and a
specific remediation. Report a result for every layer and state that static
analysis does not replace dynamic testing, penetration testing, or threat
modeling.
