package debugpanel

// cycleAttachProfile steps §41b's profile field ('p' on a pod attach panel).
func (m *Model) cycleAttachProfile() { m.attachProfile = m.attachProfile.Next() }

// cycleNodeProfile steps §41d's profile field ('p' on a node debug panel) —
// the same field the retired 's' NodeShell verb hardcoded to "sysadmin".
func (m *Model) cycleNodeProfile() { m.nodeProfile = m.nodeProfile.Next() }
