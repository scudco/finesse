# frozen_string_literal: true

require 'test_helper'

# Stub Turbo module before any test runs to avoid loading the real engine.
unless defined?(Turbo)
  module Turbo; end
  $LOADED_FEATURES << 'turbo-rails.rb'
end

class TestSigningKey < Minitest::Test
  def test_signing_key_returns_hex_encoded_key
    raw_key = 'super-secret-key'
    Turbo.define_singleton_method(:signed_stream_verifier_key) { raw_key }

    assert_equal raw_key.unpack1('H*'), Finesse.signing_key
  end
end
