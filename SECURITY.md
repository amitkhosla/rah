# Security Policy

## Supported Versions

Only the latest release of RAH receives security fixes. We recommend always upgrading to the most recent version to ensure you have the latest security patches.

| Version | Supported          |
| ------- | ------------------ |
| Latest  | ✅ Yes             |
| < Latest | ❌ No              |

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability in RAH, please report it privately to **amit.khosla.jobs@gmail.com** instead of opening a public GitHub issue.

**Do NOT open a public GitHub issue for security vulnerabilities.**

### What to Include

When reporting a vulnerability, please provide:

- **RAH version** affected
- **Steps to reproduce** the vulnerability
- **Potential impact** of the issue
- Any additional context that would be helpful

## Response Timeline

We are committed to addressing security vulnerabilities promptly:

- **Acknowledgment**: We will acknowledge receipt of your vulnerability report within **72 hours**
- **Patch Timeline**: 
  - **Critical/High severity**: patch within **14 days**
  - **Medium/Low severity**: addressed in the next regular release cycle

## In Scope

The following types of vulnerabilities are in scope for our security policy:

- Authentication bypass vulnerabilities
- JWT/token validation flaws
- Rate limiting bypass
- Privilege escalation between tenants
- Remote code execution (RCE)
- Other authentication and authorization issues

## Out of Scope

The following are **not** in scope for this security policy:

- Vulnerabilities in upstream dependencies (please report these directly to the upstream project)
- Denial of Service (DoS) attacks resulting from intentionally misconfigured rate limits or resource constraints
- Vulnerabilities in user-deployed infrastructure or misconfigured deployments
- Social engineering attacks or phishing

## Disclosure

We practice **coordinated disclosure**. Once we receive a vulnerability report:

1. We will work with you to understand and validate the issue
2. We will develop and test a fix
3. We will coordinate with you on the timing of public disclosure
4. We will credit you in the security advisory (unless you prefer anonymity)

## Continuous Security Monitoring

RAH runs security checks automatically on every CI push using `govulncheck` to identify and address vulnerabilities in dependencies proactively.

## Questions?

If you have questions about this security policy, please contact amit.khosla.jobs@gmail.com.
