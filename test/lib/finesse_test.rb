# frozen_string_literal: true

require "test_helper"

class TestSigningKey < Minitest::Test
  def test_signing_key_returns_hex_encoded_key
    raw_key = "super-secret-key"
    expected_hex = raw_key.unpack1("H*")

    # Stub Turbo before signing_key calls `require "turbo-rails"`.
    # Define the module so the require is a no-op.
    turbo_existed = defined?(Turbo)
    unless turbo_existed
      Object.const_set(:Turbo, Module.new)
    end
    original_verifier_key = Turbo.respond_to?(:signed_stream_verifier_key) ?
      Turbo.method(:signed_stream_verifier_key) : nil

    Turbo.define_singleton_method(:signed_stream_verifier_key) { raw_key }

    # Prevent `require "turbo-rails"` from loading the real engine.
    $LOADED_FEATURES << "turbo-rails.rb" unless $LOADED_FEATURES.any? { |f| f.end_with?("turbo-rails.rb") }

    assert_equal expected_hex, Finesse.signing_key
  ensure
    if original_verifier_key
      Turbo.define_singleton_method(:signed_stream_verifier_key, original_verifier_key)
    elsif turbo_existed
      Turbo.singleton_class.remove_method(:signed_stream_verifier_key) if Turbo.respond_to?(:signed_stream_verifier_key)
    else
      Object.send(:remove_const, :Turbo)
    end
  end
end
