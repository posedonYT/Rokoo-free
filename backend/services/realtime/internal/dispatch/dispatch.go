package dispatch

import "rokko.dev/pkg/eventx"

func Recipients(env eventx.Envelope, guildUsers, dmUsers func(string) []string) []string {
	if len(env.RecipientIDs) > 0 {
		return unique(env.RecipientIDs)
	}
	if env.GuildID != "" && guildUsers != nil {
		return unique(guildUsers(env.GuildID))
	}
	if env.ChannelID != "" && dmUsers != nil {
		return unique(dmUsers(env.ChannelID))
	}
	return nil
}

func unique(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
