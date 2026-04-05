class Solon < Formula
  desc "Self-hosted AI runtime with secure web API"
  homepage "https://getsolon.dev"
  license "Apache-2.0"
  version "0.0.0" # Updated by CI on release

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/getsolon/SOLON/releases/download/v#{version}/solon-darwin-arm64"
      sha256 "PLACEHOLDER" # Updated by CI
    else
      url "https://github.com/getsolon/SOLON/releases/download/v#{version}/solon-darwin-amd64"
      sha256 "PLACEHOLDER" # Updated by CI
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/getsolon/SOLON/releases/download/v#{version}/solon-linux-arm64"
      sha256 "PLACEHOLDER" # Updated by CI
    else
      url "https://github.com/getsolon/SOLON/releases/download/v#{version}/solon-linux-amd64"
      sha256 "PLACEHOLDER" # Updated by CI
    end
  end

  def install
    binary = Dir["solon-*"].first || "solon"
    bin.install binary => "solon"
  end

  test do
    assert_match "solon version", shell_output("#{bin}/solon version")
  end

  service do
    run [opt_bin/"solon", "serve"]
    keep_alive true
    log_path var/"log/solon.log"
    error_log_path var/"log/solon.log"
  end
end
