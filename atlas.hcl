# Atlas authors migrations; goose applies them (decision D19).
# internal/store/schema.sql is the desired state. `mise run db:diff <name>`
# writes a goose-format migration to internal/store/migrations.
# The dev database is a throwaway container used only to compute diffs.
env "local" {
  src = "file://internal/store/schema.sql"
  dev = "docker://postgres/17/dev?search_path=public"
  migration {
    dir = "file://internal/store/migrations?format=goose"
  }
}
