# Audit: dev back-merge (#371)

Range 0659700..ec50dbf, dev's tip, beyond batch-b's audit (#370): d139a8d,
the API merge of back-merge !346, recorded by GitLab with one parent
(983de29) and no changes; and the v0.4.8 release record (58fc569, !347).

## Result

- d139a8d changes no file. The real preview -> dev merge that replaces
  it brings preview's main -> preview merge (3ef4d00), whose content dev
  already has.
- The release record (CHANGELOG.md's [0.4.8] section and
  docs/releases/v0.4.8.md) was read: the entry matches #361's change and
  the record matches the template; release readiness reports both ok.
- No findings.

Audited-through: ec50dbf904e4daa88000d765f5339256d44d907d
