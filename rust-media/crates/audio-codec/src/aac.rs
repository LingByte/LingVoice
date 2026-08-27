//! AAC ADTS (Audio Data Transport Stream) demuxer/muxer.
//!
//! ADTS provides a framing layer for raw AAC frames, allowing a stream of
//! AAC frames to be stored or transmitted without an external container.
//! Each ADTS frame is preceded by a 7-byte header (or 9-byte if CRC is
//! present).
//!
//! This module is `no_std`-compatible (uses only `core` and `alloc`).

use crate::error::CodecError;

/// ADTS sampling frequency index table.
///
/// Index → sample rate in Hz. Index 13–15 are reserved.
pub const ADTS_SAMPLE_RATES: [u32; 13] = [
    96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350,
];

/// ADTS header (7 or 9 bytes).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct AdtsHeader {
    /// 1 = Main, 2 = LC, 3 = SSR, 4 = LTP (stored as the raw 2-bit value).
    pub profile: u8,
    /// Index into [`ADTS_SAMPLE_RATES`].
    pub sample_rate_index: u8,
    /// Channel configuration (0 = program_config_element needed).
    pub channel_config: u8,
    /// Total frame length including the header itself.
    pub frame_length: u16,
    /// Whether a 2-byte CRC follows the 7-byte header (protection_absent = 0).
    pub has_crc: bool,
}

impl AdtsHeader {
    /// Parse a 7-byte ADTS header from `data`.
    ///
    /// Returns [`CodecError::InvalidInput`] if the data is too short or the
    /// sync word does not match `0xFFF`.
    pub fn parse(data: &[u8]) -> Result<Self, CodecError> {
        if data.len() < 7 {
            return Err(CodecError::InvalidInput);
        }

        // Sync word: 12 bits = 0xFFF
        let sync = ((data[0] as u16) << 4) | ((data[1] as u16) >> 4);
        if sync != 0xFFF {
            return Err(CodecError::InvalidInput);
        }

        // protection_absent: bit 1 of data[1]
        let protection_absent = (data[1] & 0x01) != 0;
        // profile: 2 bits (data[2] >> 6)
        let profile = (data[2] >> 6) & 0x03;
        // sampling_frequency_index: 4 bits
        let sample_rate_index = (data[2] >> 2) & 0x0F;
        // channel_configuration: 3 bits (1 bit from data[2], 2 bits from data[3])
        let channel_config = ((data[2] & 0x01) << 2) | ((data[3] >> 6) & 0x03);
        // frame_length: 13 bits (2 bits from data[3], 8 bits from data[4], 3 bits from data[5])
        let frame_length =
            (((data[3] as u16) & 0x03) << 11) | ((data[4] as u16) << 3) | ((data[5] as u16) >> 5);

        Ok(Self {
            profile,
            sample_rate_index,
            channel_config,
            frame_length,
            has_crc: !protection_absent,
        })
    }

    /// Encode the header into a 7-byte array (no CRC).
    pub fn encode(&self) -> [u8; 7] {
        let mut h = [0u8; 7];

        // Sync word (12 bits) + ID (1 bit, 0=MPEG-4) + layer (2 bits, 00) + protection_absent (1 bit)
        h[0] = 0xFF; // 1111 1111
        h[1] = 0xF1; // 1111 0001 (sync low 4 + ID=0 + layer=00 + protection_absent=1)
        if self.has_crc {
            h[1] &= 0xFE; // protection_absent = 0
        }

        // profile (2 bits) + sampling_frequency_index (4 bits) + private (1 bit) + channel_config high (1 bit)
        h[2] = ((self.profile & 0x03) << 6)
            | ((self.sample_rate_index & 0x0F) << 2)
            | ((self.channel_config >> 2) & 0x01);

        // channel_config low (2 bits) + frame_length high (2 bits) + frame_length mid start
        h[3] = ((self.channel_config & 0x03) << 6) | (((self.frame_length >> 11) & 0x03) as u8);

        // frame_length middle (8 bits)
        h[4] = ((self.frame_length >> 3) & 0xFF) as u8;

        // frame_length low (3 bits) + buffer_fullness high (5 bits, 0x7FF = VBR)
        h[5] = (((self.frame_length & 0x07) << 5) as u8) | 0x1F;

        // buffer_fullness low (6 bits) + number_of_raw_data_blocks (2 bits, 0 = 1 block)
        h[6] = 0xFC; // 1111 1100

        h
    }

    /// Return the sample rate in Hz for this header.
    pub fn sample_rate(&self) -> u32 {
        if (self.sample_rate_index as usize) < ADTS_SAMPLE_RATES.len() {
            ADTS_SAMPLE_RATES[self.sample_rate_index as usize]
        } else {
            0
        }
    }

    /// Return the number of channels for this header.
    pub fn channels(&self) -> u16 {
        self.channel_config as u16
    }

    /// Header size in bytes (7 without CRC, 9 with CRC).
    pub fn header_size(&self) -> usize {
        if self.has_crc {
            9
        } else {
            7
        }
    }
}

/// AAC ADTS demuxer — splits a raw byte stream into individual ADTS frames.
#[derive(Debug, Default)]
pub struct AacAdtsDemuxer {
    buffer: Vec<u8>,
    /// Stores the payload of the most recently extracted frame so that
    /// `next_frame` can return a `&[u8]` reference.
    last_payload: Vec<u8>,
}

impl AacAdtsDemuxer {
    /// Create a new empty demuxer.
    pub fn new() -> Self {
        Self {
            buffer: Vec::new(),
            last_payload: Vec::new(),
        }
    }

    /// Append more data to the internal buffer.
    pub fn push(&mut self, data: &[u8]) {
        self.buffer.extend_from_slice(data);
    }

    /// Attempt to extract the next ADTS frame.
    ///
    /// Returns `Some((header, payload))` where `payload` is the raw AAC frame
    /// data (without the ADTS header), or `None` if not enough data is
    /// available.
    pub fn next_frame(&mut self) -> Option<(AdtsHeader, &[u8])> {
        loop {
            // Need at least 7 bytes for the header.
            if self.buffer.len() < 7 {
                return None;
            }

            // Find sync word (0xFFF). Scan for it if not at the current position.
            let sync_pos = self
                .buffer
                .windows(2)
                .position(|w| (w[0] == 0xFF) && ((w[1] & 0xF0) == 0xF0));

            match sync_pos {
                Some(0) => {}
                Some(pos) => {
                    // Discard bytes before the sync word.
                    self.buffer.drain(0..pos);
                    continue;
                }
                None => {
                    // Keep the last byte (could be the high byte of a sync word).
                    let keep = self.buffer.len().saturating_sub(1);
                    self.buffer.drain(0..keep);
                    return None;
                }
            }

            // Parse the header.
            let header = AdtsHeader::parse(&self.buffer).ok()?;

            let total_len = header.frame_length as usize;
            if total_len < header.header_size() {
                // Invalid frame length; discard the sync byte and retry.
                self.buffer.drain(0..1);
                continue;
            }

            if self.buffer.len() < total_len {
                // Not enough data yet.
                return None;
            }

            // Extract the payload (data after the header, up to frame_length).
            let header_size = header.header_size();
            self.last_payload.clear();
            self.last_payload
                .extend_from_slice(&self.buffer[header_size..total_len]);

            // Drain consumed bytes from the input buffer.
            self.buffer.drain(0..total_len);

            return Some((header, &self.last_payload));
        }
    }
}

/// AAC ADTS muxer — wraps raw AAC frames with ADTS headers.
#[derive(Debug, Clone, Copy)]
pub struct AacAdtsMuxer {
    profile: u8,
    sample_rate_index: u8,
    channel_config: u8,
}

impl AacAdtsMuxer {
    /// Create a new muxer for the given sample rate and channel count.
    ///
    /// If the sample rate is not one of the standard ADTS rates, it falls
    /// back to index 4 (44100 Hz).
    pub fn new(sample_rate: u32, channels: u16) -> Self {
        let sample_rate_index = sample_rate_to_index(sample_rate);
        let channel_config = if channels == 0 { 1 } else { channels as u8 };
        // Default to LC (Low Complexity) profile = 2.
        Self {
            profile: 2,
            sample_rate_index,
            channel_config,
        }
    }

    /// Wrap a raw AAC frame with an ADTS header.
    pub fn mux_frame(&self, aac_data: &[u8]) -> Vec<u8> {
        let header_size = 7u16;
        let frame_length = header_size + aac_data.len() as u16;

        let header = AdtsHeader {
            profile: self.profile,
            sample_rate_index: self.sample_rate_index,
            channel_config: self.channel_config,
            frame_length,
            has_crc: false,
        };

        let mut out = Vec::with_capacity(frame_length as usize);
        out.extend_from_slice(&header.encode());
        out.extend_from_slice(aac_data);
        out
    }
}

/// Map a sample rate (Hz) to the nearest ADTS sampling frequency index.
pub fn sample_rate_to_index(rate: u32) -> u8 {
    for (i, &r) in ADTS_SAMPLE_RATES.iter().enumerate() {
        if r == rate {
            return i as u8;
        }
    }
    // Default to 44100 (index 4) if not found.
    4
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_adts_header_parse() {
        // Construct a valid 7-byte ADTS header for LC profile, 44100 Hz, stereo.
        let header = AdtsHeader {
            profile: 2,
            sample_rate_index: 4, // 44100
            channel_config: 2,
            frame_length: 100,
            has_crc: false,
        };
        let bytes = header.encode();
        let parsed = AdtsHeader::parse(&bytes).unwrap();
        assert_eq!(parsed.profile, 2);
        assert_eq!(parsed.sample_rate_index, 4);
        assert_eq!(parsed.channel_config, 2);
        assert_eq!(parsed.frame_length, 100);
        assert!(!parsed.has_crc);
    }

    #[test]
    fn test_adts_header_parse_invalid_sync() {
        let data = [0x00u8; 7];
        assert_eq!(AdtsHeader::parse(&data), Err(CodecError::InvalidInput));
    }

    #[test]
    fn test_adts_header_parse_too_short() {
        let data = [0xFFu8, 0xF1];
        assert_eq!(AdtsHeader::parse(&data), Err(CodecError::InvalidInput));
    }

    #[test]
    fn test_adts_header_encode() {
        let header = AdtsHeader {
            profile: 1,           // Main
            sample_rate_index: 3, // 48000
            channel_config: 1,
            frame_length: 200,
            has_crc: false,
        };
        let bytes = header.encode();

        // Verify sync word
        assert_eq!(bytes[0], 0xFF);
        assert_eq!(bytes[1] & 0xF0, 0xF0);
        // protection_absent = 1 (no CRC)
        assert_eq!(bytes[1] & 0x01, 0x01);

        // Verify profile (bits 6-7 of byte 2)
        assert_eq!((bytes[2] >> 6) & 0x03, 1);
        // Verify sample rate index (bits 2-5 of byte 2)
        assert_eq!((bytes[2] >> 2) & 0x0F, 3);
        // Verify frame_length
        let parsed = AdtsHeader::parse(&bytes).unwrap();
        assert_eq!(parsed.frame_length, 200);
    }

    #[test]
    fn test_adts_header_encode_with_crc() {
        let header = AdtsHeader {
            profile: 2,
            sample_rate_index: 4,
            channel_config: 2,
            frame_length: 50,
            has_crc: true,
        };
        let bytes = header.encode();
        // protection_absent = 0 when has_crc = true
        assert_eq!(bytes[1] & 0x01, 0x00);
        assert_eq!(header.header_size(), 9);
    }

    #[test]
    fn test_adts_demux() {
        let mut demuxer = AacAdtsDemuxer::new();

        // Create two ADTS frames with different payloads.
        let muxer = AacAdtsMuxer::new(44100, 2);
        let frame1 = muxer.mux_frame(&[0xAA; 50]);
        let frame2 = muxer.mux_frame(&[0xBB; 30]);

        // Push both frames at once.
        demuxer.push(&frame1);
        demuxer.push(&frame2);

        // Extract first frame.
        let (h1, p1) = demuxer.next_frame().unwrap();
        assert_eq!(h1.sample_rate(), 44100);
        assert_eq!(h1.channels(), 2);
        assert_eq!(p1.len(), 50);
        assert_eq!(p1[0], 0xAA);

        // Extract second frame.
        let (_h2, p2) = demuxer.next_frame().unwrap();
        assert_eq!(p2.len(), 30);
        assert_eq!(p2[0], 0xBB);

        // No more frames.
        assert!(demuxer.next_frame().is_none());
    }

    #[test]
    fn test_adts_demux_partial() {
        let mut demuxer = AacAdtsDemuxer::new();
        let muxer = AacAdtsMuxer::new(48000, 1);
        let frame = muxer.mux_frame(&[0xCC; 100]);

        // Push only part of the frame.
        demuxer.push(&frame[..10]);
        assert!(demuxer.next_frame().is_none());

        // Push the rest.
        demuxer.push(&frame[10..]);
        let (h, p) = demuxer.next_frame().unwrap();
        assert_eq!(p.len(), 100);
        assert_eq!(h.sample_rate(), 48000);
    }

    #[test]
    fn test_adts_demux_with_garbage() {
        let mut demuxer = AacAdtsDemuxer::new();
        let muxer = AacAdtsMuxer::new(44100, 1);
        let frame = muxer.mux_frame(&[0xDD; 40]);

        // Prepend some garbage bytes.
        let mut data = vec![0x00, 0x01, 0x02];
        data.extend_from_slice(&frame);
        demuxer.push(&data);

        let (h, p) = demuxer.next_frame().unwrap();
        assert_eq!(p.len(), 40);
        assert_eq!(p[0], 0xDD);
        assert_eq!(h.sample_rate(), 44100);
    }

    #[test]
    fn test_adts_mux() {
        let muxer = AacAdtsMuxer::new(44100, 2);
        let payload = [0x12, 0x34, 0x56, 0x78];
        let frame = muxer.mux_frame(&payload);

        // Frame should be 7 (header) + 4 (payload) = 11 bytes.
        assert_eq!(frame.len(), 11);

        // Verify it can be parsed back.
        let header = AdtsHeader::parse(&frame).unwrap();
        assert_eq!(header.frame_length, 11);
        assert_eq!(header.sample_rate(), 44100);
        assert_eq!(header.channels(), 2);

        // Payload should match.
        assert_eq!(&frame[7..], &payload);
    }

    #[test]
    fn test_sample_rate_index() {
        assert_eq!(sample_rate_to_index(96000), 0);
        assert_eq!(sample_rate_to_index(88200), 1);
        assert_eq!(sample_rate_to_index(64000), 2);
        assert_eq!(sample_rate_to_index(48000), 3);
        assert_eq!(sample_rate_to_index(44100), 4);
        assert_eq!(sample_rate_to_index(32000), 5);
        assert_eq!(sample_rate_to_index(24000), 6);
        assert_eq!(sample_rate_to_index(22050), 7);
        assert_eq!(sample_rate_to_index(16000), 8);
        assert_eq!(sample_rate_to_index(12000), 9);
        assert_eq!(sample_rate_to_index(11025), 10);
        assert_eq!(sample_rate_to_index(8000), 11);
        assert_eq!(sample_rate_to_index(7350), 12);
        // Unknown rate defaults to 4 (44100).
        assert_eq!(sample_rate_to_index(12345), 4);
    }

    #[test]
    fn test_sample_rate_from_header() {
        let h = AdtsHeader {
            profile: 2,
            sample_rate_index: 0,
            channel_config: 1,
            frame_length: 7,
            has_crc: false,
        };
        assert_eq!(h.sample_rate(), 96000);

        let h2 = AdtsHeader {
            profile: 2,
            sample_rate_index: 11,
            channel_config: 1,
            frame_length: 7,
            has_crc: false,
        };
        assert_eq!(h2.sample_rate(), 8000);
    }
}
