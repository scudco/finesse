# frozen_string_literal: true

require_relative "finesse/version"
require_relative "finesse/platforms"

module Finesse
  # Returns the Turbo signed stream verifier key as a hex string.
  # Called by the exe wrapper via `rails runner` to set FINESSE_SIGNING_KEY.
  def self.signing_key
    require "turbo-rails"
    Turbo.signed_stream_verifier_key.unpack1("H*")
  end
end
