-- +goose Up
-- modify "gate_results" table
ALTER TABLE "gate_results" DROP CONSTRAINT "gate_results_gate_type_check", ADD CONSTRAINT "gate_results_gate_type_check" CHECK (gate_type = ANY (ARRAY['command'::text, 'agent-verdict'::text, 'human'::text, 'provider-checks'::text]));

-- +goose Down
-- reverse: modify "gate_results" table
ALTER TABLE "gate_results" DROP CONSTRAINT "gate_results_gate_type_check", ADD CONSTRAINT "gate_results_gate_type_check" CHECK (gate_type = ANY (ARRAY['command'::text, 'agent-verdict'::text, 'human'::text]));
