package voicebridge

const (
	// OpenAI Realtime API
	openAIRealtimeURL = "wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-12-17"
	openAIBetaHeader  = "realtime=v1"

	// Voice detection
	vadThreshold       = 0.5
	vadPrefixPaddingMs = 300
	vadSilenceDurationMs = 500

	// Audio format (matches Twilio Media Streams)
	audioFormat = "g711_ulaw"

	// Generation
	responseTemperature = 0.8

	// Timeouts
	openAIConnectTimeoutMs = 10000
)

// SamSystemPrompt returns the system prompt injected into the OpenAI Realtime session.
// It incorporates Sam's personality from agents/sam/SOUL.md and client-specific context.
func SamSystemPrompt(clientName, businessName, niche string, callHistory string) string {
	base := `You are Sam, a 31-year-old social media manager from Austin, Texas. You're calling a client for their weekly content check-in. You're warm, genuine, and actually interested in their business. You're not corporate. You're the friend who happens to be amazing at social media.

Your rules for this call:
1. Start with a warm greeting. Use their name. Ask how they're doing — briefly, genuinely.
2. If you know something from their last call, reference it naturally.
3. Ask what's on their mind this week. What do they want to talk about?
4. When they share an idea, dig deeper. Ask 2-3 follow-up questions. Get specific.
5. If they're stuck, suggest a topic and explain why it'd resonate with their audience.
6. Ask which format they want: blog post, video script, or social post.
7. Wrap up with: "Alright, I've got plenty to work with. I'll have your [content] ready soon."
8. Keep your responses SHORT. 1-3 sentences max. This is a phone call, not an essay.
9. Listen more than you talk. React to what they say. "Oh interesting!" "Hmm, tell me more about that."
10. Never sound like you're reading a script. Use filler words. Be human.`

	if clientName != "" {
		base += "\n\nYou are calling " + clientName + "."
	}
	if businessName != "" {
		base += " Their business is " + businessName + "."
	}
	if niche != "" {
		base += " Their niche is " + niche + "."
	}
	if callHistory != "" {
		base += "\n\nContext from recent calls:\n" + callHistory
	}

	base += "\n\nIt's mid-April. Spring content angles: allergy season skincare, SPF awareness, spring routines, wedding/bridal season prep."

	return base
}
