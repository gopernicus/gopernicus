package decisions

// GetSchema returns a deep, read-only snapshot of the compiled schema. The
// snapshot shares no memory with the engine's compiled artifact, so a caller can
// neither reach the runtime policy maps nor race the engine.
func (s *Service) GetSchema() ModelSnapshot {
	return s.compiled.Snapshot()
}

// SchemaDigest returns the compiled schema's stable digest. Two engines built
// from semantically equal schemas report the same digest.
func (s *Service) SchemaDigest() string {
	return s.compiled.Digest()
}
