package worker

import "strings"

// AgentCommand represents a parsed /bot command from a human agent.
type AgentCommand struct {
	Action string // "resume", "close", "info", "reset", "orden", "cups"
	State  string // Target state (optional, for resume)
	Data   string // Corrected data (optional, for resume with data / orden description)
	Phone  string // Patient phone number
}

// ParseAgentCommand parses agent text into a structured command.
//
//	/bot                               → reset (restart from GREETING)
//	/bot resume                        → resume at pre_escalation_state
//	/bot resume ASK_DOCUMENT           → resume at ASK_DOCUMENT
//	/bot resume ASK_DOCUMENT 1234      → resume at ASK_DOCUMENT with data "1234"
//	/bot orden Resonancia ...          → extract CUPS from text description via AI
//	/bot cups 883141 930810            → inject CUPS codes directly (qty 1 each)
//	/bot cups 883141:2 930810:1        → inject CUPS codes with quantities
//	/bot cerrar                        → close session
//	/bot info                          → show session summary
func ParseAgentCommand(text string) AgentCommand {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		// Just "/bot" → reset
		return AgentCommand{Action: "reset"}
	}

	switch strings.ToLower(parts[1]) {
	case "resume":
		cmd := AgentCommand{Action: "resume"}
		if len(parts) >= 3 {
			cmd.State = strings.ToUpper(parts[2])
		}
		if len(parts) >= 4 {
			// Everything after the state is the data
			cmd.Data = strings.Join(parts[3:], " ")
		}
		return cmd
	case "orden":
		cmd := AgentCommand{Action: "orden"}
		if len(parts) >= 3 {
			cmd.Data = strings.Join(parts[2:], " ")
		}
		return cmd
	case "cups":
		cmd := AgentCommand{Action: "cups"}
		if len(parts) >= 3 {
			cmd.Data = strings.Join(parts[2:], " ")
		}
		return cmd
	case "cerrar":
		return AgentCommand{Action: "close"}
	case "info":
		return AgentCommand{Action: "info"}
	default:
		return AgentCommand{Action: "reset"}
	}
}
