# Creates Mastodon's local users, named on the command line, and prints an API
# token for each as name=token. Run with bin/rails runner (mastodon.go).
#
# Users are saved without validation: Mastodon checks that an email domain
# has mail servers, which the fake network's don't.
ARGV.each do |name|
  account = Account.find_local(name) || Account.new(username: name)
  user = account.user || User.new(email: "#{name}@#{Rails.configuration.x.local_domain}",
                                  password: SecureRandom.hex(16), agreement: true, approved: true,
                                  confirmed_at: Time.now.utc, account: account, bypass_registration_checks: true)
  user.save!(validate: false)
  # Saving a user sets approved from the registration mode, closed here.
  user.update_columns(approved: true)
  app = Doorkeeper::Application.find_or_create_by!(name: 'judge') do |a|
    a.redirect_uri = 'urn:ietf:wg:oauth:2.0:oob'
    a.scopes = 'read write follow'
  end
  token = Doorkeeper::AccessToken.create!(application: app, resource_owner_id: user.id, scopes: 'read write follow')
  puts "#{name}=#{token.token}"
end
