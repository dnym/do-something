# Security

This is an offline personal-data tool. Database files, snapshots, JSON exports,
and archives are unencrypted. Protect them with operating-system permissions and
a trusted synchronization service. Do not import or synchronize untrusted files.

Database UUIDs prevent accidental mixups; they are not authentication. Validation
checks format and consistency, not authorship or authorization. A malicious
writer with access to the sync directory is outside the protection boundary.
The application does not execute imported strings, shell commands, or URLs.

Report security concerns privately to the repository maintainer using GitHub's
private vulnerability reporting when enabled. Do not attach personal databases
or spreadsheet exports to public issues. Include a minimal synthetic reproducer,
platform, application version, and relevant error codes.
