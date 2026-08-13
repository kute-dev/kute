package browse

import (
	"net"

	tea "charm.land/bubbletea/v2"
)

// copySelectedServiceAddress copies one Service address and its first port.
func (m Model) copySelectedServiceAddress(addressCell int) tea.Cmd {
	row, ok := m.selectedRow()
	if !ok || len(row.Cells) <= addressCell || len(row.Cells) <= 4 {
		return nil
	}
	address := firstServicePort(row.Cells[addressCell])
	port := firstServicePort(row.Cells[4])
	if address == "" || address == "<none>" || address == "None" || port == "" {
		return nil
	}
	return tea.SetClipboard(net.JoinHostPort(address, port))
}

func firstServicePort(ports string) string {
	for i, r := range ports {
		if r == ',' {
			return ports[:i]
		}
	}
	return ports
}
