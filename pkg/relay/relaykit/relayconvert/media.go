package relayconvert

import relaymedia "github.com/LingByte/LingVoice/pkg/relay/relaykit/relayconvert/internal/media"

type MediaResolver = relaymedia.MediaResolver

func SetMediaResolver(resolver MediaResolver) {
	relaymedia.SetMediaResolver(resolver)
}
