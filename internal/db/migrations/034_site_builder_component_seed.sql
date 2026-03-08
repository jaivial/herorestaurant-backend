-- Site Builder Component Registry Seed Data
-- Defines all core components available in the visual editor

-- Layout Components
INSERT INTO site_builder_component_registry (type, category, label, description, props_schema, style_schema, bindings_schema, nesting_rules, icon, is_active, sort_order) VALUES
('header', 'layout', 'Header', 'Site header with navigation and logo', 
 JSON_OBJECT(
   'logoUrl', JSON_OBJECT('type', 'string', 'label', 'Logo URL', 'required', false),
   'logoAlt', JSON_OBJECT('type', 'string', 'label', 'Logo Alt Text', 'required', false),
   'sticky', JSON_OBJECT('type', 'boolean', 'label', 'Sticky Header', 'default', true),
   'navItems', JSON_OBJECT('type', 'array', 'label', 'Navigation Items', 'itemSchema', JSON_OBJECT('label', JSON_OBJECT('type', 'string'), 'href', JSON_OBJECT('type', 'string'), 'target', JSON_OBJECT('type', 'string', 'enum', JSON_ARRAY('_self', '_blank'))))
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top', 'min', 0, 'max', 100),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom', 'min', 0, 'max', 100)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY('nav-menu', 'logo', 'text'), 'allowedParents', JSON_ARRAY('page')),
 'layout', 1, 10),

('footer', 'layout', 'Footer', 'Site footer with links and copyright', 
 JSON_OBJECT(
   'copyrightText', JSON_OBJECT('type', 'string', 'label', 'Copyright Text'),
   'showSocialLinks', JSON_OBJECT('type', 'boolean', 'label', 'Show Social Links', 'default', true),
   'columns', JSON_OBJECT('type', 'number', 'label', 'Columns', 'min', 1, 'max', 4, 'default', 3)
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top', 'min', 0, 'max', 200),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom', 'min', 0, 'max', 200)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY('text', 'nav-menu', 'social-links'), 'allowedParents', JSON_ARRAY('page')),
 'layout', 1, 11),

('section', 'layout', 'Section', 'Generic content section', 
 JSON_OBJECT(
   'containerWidth', JSON_OBJECT('type', 'string', 'label', 'Container Width', 'enum', JSON_ARRAY('sm', 'md', 'lg', 'xl', 'full'), 'default', 'lg'),
   'fullHeight', JSON_OBJECT('type', 'boolean', 'label', 'Full Height', 'default', false)
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'backgroundImage', JSON_OBJECT('type', 'string', 'label', 'Background Image URL'),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top', 'min', 0, 'max', 200),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom', 'min', 0, 'max', 200)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY('*'), 'allowedParents', JSON_ARRAY('page', 'section')),
 'square', 1, 12),

('columns', 'layout', 'Columns', 'Multi-column layout', 
 JSON_OBJECT(
   'columnCount', JSON_OBJECT('type', 'number', 'label', 'Number of Columns', 'min', 1, 'max', 6, 'default', 2),
   'gap', JSON_OBJECT('type', 'number', 'label', 'Gap', 'min', 0, 'max', 100, 'default', 24),
   'mobileStack', JSON_OBJECT('type', 'boolean', 'label', 'Stack on Mobile', 'default', true)
 ),
 JSON_OBJECT(
   'alignItems', JSON_OBJECT('type', 'string', 'label', 'Align Items', 'enum', JSON_ARRAY('start', 'center', 'end', 'stretch'))
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY('*'), 'allowedParents', JSON_ARRAY('section', 'columns')),
 'columns', 1, 13),

-- Content Components
('hero', 'content', 'Hero', 'Large hero section with title and CTA', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Title', 'required', true),
   'subtitle', JSON_OBJECT('type', 'string', 'label', 'Subtitle'),
   'backgroundImage', JSON_OBJECT('type', 'string', 'label', 'Background Image URL'),
   'buttonText', JSON_OBJECT('type', 'string', 'label', 'Button Text'),
   'buttonHref', JSON_OBJECT('type', 'string', 'label', 'Button Link'),
   'buttonTarget', JSON_OBJECT('type', 'string', 'label', 'Button Target', 'enum', JSON_ARRAY('_self', '_blank'), 'default', '_self'),
   'align', JSON_OBJECT('type', 'string', 'label', 'Alignment', 'enum', JSON_ARRAY('left', 'center', 'right'), 'default', 'center'),
   'overlay', JSON_OBJECT('type', 'boolean', 'label', 'Dark Overlay', 'default', true)
 ),
 JSON_OBJECT(
   'minHeight', JSON_OBJECT('type', 'number', 'label', 'Min Height (vh)', 'min', 30, 'max', 100, 'default', 70),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top', 'min', 0, 'max', 200),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom', 'min', 0, 'max', 200)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('section')),
 'star', 1, 20),

('text', 'content', 'Text Block', 'Rich text content block', 
 JSON_OBJECT(
   'content', JSON_OBJECT('type', 'richtext', 'label', 'Content', 'required', true),
   'align', JSON_OBJECT('type', 'string', 'label', 'Text Align', 'enum', JSON_ARRAY('left', 'center', 'right', 'justify'), 'default', 'left')
 ),
 JSON_OBJECT(
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'fontSize', JSON_OBJECT('type', 'string', 'label', 'Font Size', 'enum', JSON_ARRAY('sm', 'base', 'lg', 'xl', '2xl')),
   'lineHeight', JSON_OBJECT('type', 'number', 'label', 'Line Height', 'min', 1, 'max', 3)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'type', 1, 21),

('image', 'content', 'Image', 'Image with optional caption', 
 JSON_OBJECT(
   'src', JSON_OBJECT('type', 'string', 'label', 'Image URL', 'required', true),
   'alt', JSON_OBJECT('type', 'string', 'label', 'Alt Text', 'required', true),
   'caption', JSON_OBJECT('type', 'string', 'label', 'Caption'),
   'link', JSON_OBJECT('type', 'string', 'label', 'Link URL'),
   'lazyLoad', JSON_OBJECT('type', 'boolean', 'label', 'Lazy Load', 'default', true)
 ),
 JSON_OBJECT(
   'objectFit', JSON_OBJECT('type', 'string', 'label', 'Object Fit', 'enum', JSON_ARRAY('cover', 'contain', 'fill', 'none')),
   'borderRadius', JSON_OBJECT('type', 'number', 'label', 'Border Radius', 'min', 0, 'max', 50)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'image', 1, 22),

('heading', 'content', 'Heading', 'Section heading', 
 JSON_OBJECT(
   'text', JSON_OBJECT('type', 'string', 'label', 'Text', 'required', true),
   'level', JSON_OBJECT('type', 'number', 'label', 'Heading Level', 'enum', JSON_ARRAY(1, 2, 3, 4, 5, 6), 'default', 2),
   'align', JSON_OBJECT('type', 'string', 'label', 'Alignment', 'enum', JSON_ARRAY('left', 'center', 'right'), 'default', 'left')
 ),
 JSON_OBJECT(
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'fontWeight', JSON_OBJECT('type', 'string', 'label', 'Font Weight', 'enum', JSON_ARRAY('normal', 'medium', 'semibold', 'bold'))
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'heading', 1, 23),

('button', 'content', 'Button', 'Clickable button or link', 
 JSON_OBJECT(
   'text', JSON_OBJECT('type', 'string', 'label', 'Button Text', 'required', true),
   'href', JSON_OBJECT('type', 'string', 'label', 'Link URL'),
   'target', JSON_OBJECT('type', 'string', 'label', 'Target', 'enum', JSON_ARRAY('_self', '_blank'), 'default', '_self'),
   'variant', JSON_OBJECT('type', 'string', 'label', 'Style Variant', 'enum', JSON_ARRAY('primary', 'secondary', 'outline', 'ghost'), 'default', 'primary'),
   'size', JSON_OBJECT('type', 'string', 'label', 'Size', 'enum', JSON_ARRAY('sm', 'md', 'lg'), 'default', 'md'),
   'icon', JSON_OBJECT('type', 'string', 'label', 'Icon Name')
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'borderRadius', JSON_OBJECT('type', 'number', 'label', 'Border Radius', 'min', 0, 'max', 50)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'mouse-pointer-click', 1, 24),

('spacer', 'content', 'Spacer', 'Vertical spacing element', 
 JSON_OBJECT(
   'height', JSON_OBJECT('type', 'number', 'label', 'Height (px)', 'min', 0, 'max', 500, 'default', 48),
   'mobileHeight', JSON_OBJECT('type', 'number', 'label', 'Mobile Height (px)', 'min', 0, 'max', 200)
 ),
 NULL,
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'space', 1, 25),

('divider', 'content', 'Divider', 'Horizontal divider line', 
 JSON_OBJECT(
   'style', JSON_OBJECT('type', 'string', 'label', 'Style', 'enum', JSON_ARRAY('solid', 'dashed', 'dotted'), 'default', 'solid'),
   'width', JSON_OBJECT('type', 'string', 'label', 'Width', 'enum', JSON_ARRAY('full', 'lg', 'md', 'sm'), 'default', 'full')
 ),
 JSON_OBJECT(
   'color', JSON_OBJECT('type', 'color', 'label', 'Color'),
   'thickness', JSON_OBJECT('type', 'number', 'label', 'Thickness', 'min', 1, 'max', 10, 'default', 1)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'minus', 1, 26),

('testimonial', 'content', 'Testimonial', 'Customer testimonial card', 
 JSON_OBJECT(
   'quote', JSON_OBJECT('type', 'string', 'label', 'Quote', 'required', true),
   'author', JSON_OBJECT('type', 'string', 'label', 'Author Name', 'required', true),
   'authorRole', JSON_OBJECT('type', 'string', 'label', 'Author Role'),
   'avatarUrl', JSON_OBJECT('type', 'string', 'label', 'Avatar URL'),
   'rating', JSON_OBJECT('type', 'number', 'label', 'Rating', 'min', 1, 'max', 5)
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'borderRadius', JSON_OBJECT('type', 'number', 'label', 'Border Radius', 'min', 0, 'max', 50)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'quote', 1, 27),

-- Dynamic Components (with bindings)
('property-grid', 'dynamic', 'Property Grid', 'Grid of properties from database', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Section Title'),
   'columns', JSON_OBJECT('type', 'number', 'label', 'Columns', 'min', 1, 'max', 4, 'default', 3),
   'showPrice', JSON_OBJECT('type', 'boolean', 'label', 'Show Price', 'default', true),
   'showLocation', JSON_OBJECT('type', 'boolean', 'label', 'Show Location', 'default', true),
   'cardStyle', JSON_OBJECT('type', 'string', 'label', 'Card Style', 'enum', JSON_ARRAY('card', 'minimal', 'featured'), 'default', 'card')
 ),
 JSON_OBJECT(
   'gap', JSON_OBJECT('type', 'number', 'label', 'Gap', 'min', 0, 'max', 50, 'default', 24),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top'),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom')
 ),
 JSON_OBJECT(
   'resource', JSON_OBJECT('type', 'string', 'label', 'Data Source', 'enum', JSON_ARRAY('properties.latest', 'properties.featured', 'properties.search'), 'required', true),
   'limit', JSON_OBJECT('type', 'number', 'label', 'Max Items', 'min', 1, 'max', 50, 'default', 6),
   'refreshMode', JSON_OBJECT('type', 'string', 'label', 'Refresh Mode', 'enum', JSON_ARRAY('publish', 'load'), 'default', 'publish')
 ),
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('section')),
 'building', 1, 40),

('testimonial-grid', 'dynamic', 'Testimonial Grid', 'Grid of testimonials', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Section Title'),
   'columns', JSON_OBJECT('type', 'number', 'label', 'Columns', 'min', 1, 'max', 4, 'default', 3),
   'showRating', JSON_OBJECT('type', 'boolean', 'label', 'Show Rating', 'default', true),
   'showAvatar', JSON_OBJECT('type', 'boolean', 'label', 'Show Avatar', 'default', true)
 ),
 JSON_OBJECT(
   'gap', JSON_OBJECT('type', 'number', 'label', 'Gap', 'min', 0, 'max', 50, 'default', 24),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top'),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom')
 ),
 JSON_OBJECT(
   'resource', JSON_OBJECT('type', 'string', 'label', 'Data Source', 'enum', JSON_ARRAY('testimonials.all', 'testimonials.featured'), 'required', true),
   'limit', JSON_OBJECT('type', 'number', 'label', 'Max Items', 'min', 1, 'max', 20, 'default', 6)
 ),
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('section')),
 'message-square', 1, 41),

('blog-posts', 'dynamic', 'Blog Posts', 'List of blog posts', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Section Title'),
   'layout', JSON_OBJECT('type', 'string', 'label', 'Layout', 'enum', JSON_ARRAY('grid', 'list', 'featured'), 'default', 'grid'),
   'columns', JSON_OBJECT('type', 'number', 'label', 'Columns (grid)', 'min', 1, 'max', 4, 'default', 3),
   'showExcerpt', JSON_OBJECT('type', 'boolean', 'label', 'Show Excerpt', 'default', true),
   'showDate', JSON_OBJECT('type', 'boolean', 'label', 'Show Date', 'default', true),
   'showAuthor', JSON_OBJECT('type', 'boolean', 'label', 'Show Author', 'default', false)
 ),
 JSON_OBJECT(
   'gap', JSON_OBJECT('type', 'number', 'label', 'Gap', 'min', 0, 'max', 50, 'default', 24)
 ),
 JSON_OBJECT(
   'resource', JSON_OBJECT('type', 'string', 'label', 'Data Source', 'enum', JSON_ARRAY('blog.latest', 'blog.category', 'blog.featured'), 'required', true),
   'limit', JSON_OBJECT('type', 'number', 'label', 'Max Items', 'min', 1, 'max', 20, 'default', 6),
   'category', JSON_OBJECT('type', 'string', 'label', 'Category Slug (if using category source)')
 ),
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('section')),
 'file-text', 1, 42),

('menu-display', 'dynamic', 'Menu Display', 'Display restaurant menu', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Section Title'),
   'showPrices', JSON_OBJECT('type', 'boolean', 'label', 'Show Prices', 'default', true),
   'showDescriptions', JSON_OBJECT('type', 'boolean', 'label', 'Show Descriptions', 'default', true),
   'groupByCategory', JSON_OBJECT('type', 'boolean', 'label', 'Group by Category', 'default', true)
 ),
 JSON_OBJECT(
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top'),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom')
 ),
 JSON_OBJECT(
   'resource', JSON_OBJECT('type', 'string', 'label', 'Data Source', 'enum', JSON_ARRAY('menu.regular', 'menu.special', 'menu.wine'), 'required', true),
   'menuId', JSON_OBJECT('type', 'string', 'label', 'Menu ID (optional)')
 ),
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('section')),
 'utensils', 1, 43),

-- Form Components
('contact-form', 'forms', 'Contact Form', 'Contact form with fields', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Form Title'),
   'showPhone', JSON_OBJECT('type', 'boolean', 'label', 'Show Phone Field', 'default', true),
   'showCompany', JSON_OBJECT('type', 'boolean', 'label', 'Show Company Field', 'default', false),
   'submitButtonText', JSON_OBJECT('type', 'string', 'label', 'Submit Button Text', 'default', 'Send Message'),
   'successMessage', JSON_OBJECT('type', 'string', 'label', 'Success Message', 'default', 'Thank you! We will contact you soon.'),
   'emailTo', JSON_OBJECT('type', 'string', 'label', 'Send Emails To')
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top'),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom')
 ),
 JSON_OBJECT(
   'storeSubmissions', JSON_OBJECT('type', 'boolean', 'label', 'Store in Database', 'default', true),
   'sendEmail', JSON_OBJECT('type', 'boolean', 'label', 'Send Email Notification', 'default', true)
 ),
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('section')),
 'mail', 1, 50),

('newsletter-form', 'forms', 'Newsletter Form', 'Email subscription form', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Title', 'default', 'Subscribe to our newsletter'),
   'description', JSON_OBJECT('type', 'string', 'label', 'Description'),
   'placeholder', JSON_OBJECT('type', 'string', 'label', 'Email Placeholder', 'default', 'Enter your email'),
   'submitButtonText', JSON_OBJECT('type', 'string', 'label', 'Submit Button Text', 'default', 'Subscribe'),
   'successMessage', JSON_OBJECT('type', 'string', 'label', 'Success Message', 'default', 'Thank you for subscribing!')
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'align', JSON_OBJECT('type', 'string', 'label', 'Alignment', 'enum', JSON_ARRAY('left', 'center', 'right'), 'default', 'center')
 ),
 JSON_OBJECT(
   'listId', JSON_OBJECT('type', 'string', 'label', 'Mailing List ID')
 ),
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'send', 1, 51),

('booking-form', 'forms', 'Booking Form', 'Reservation booking form', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Form Title'),
   'showGuests', JSON_OBJECT('type', 'boolean', 'label', 'Show Guests Field', 'default', true),
   'showTime', JSON_OBJECT('type', 'boolean', 'label', 'Show Time Field', 'default', true),
   'showPhone', JSON_OBJECT('type', 'boolean', 'label', 'Show Phone Field', 'default', true),
   'submitButtonText', JSON_OBJECT('type', 'string', 'label', 'Submit Button Text', 'default', 'Book Now')
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color')
 ),
 JSON_OBJECT(
   'restaurantId', JSON_OBJECT('type', 'number', 'label', 'Restaurant ID', 'required', true)
 ),
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('section')),
 'calendar', 1, 52),

-- Special Components
('map', 'content', 'Map', 'Embedded map location', 
 JSON_OBJECT(
   'address', JSON_OBJECT('type', 'string', 'label', 'Address'),
   'latitude', JSON_OBJECT('type', 'number', 'label', 'Latitude'),
   'longitude', JSON_OBJECT('type', 'number', 'label', 'Longitude'),
   'zoom', JSON_OBJECT('type', 'number', 'label', 'Zoom Level', 'min', 1, 'max', 20, 'default', 15),
   'height', JSON_OBJECT('type', 'number', 'label', 'Height (px)', 'min', 200, 'max', 800, 'default', 400)
 ),
 NULL,
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'map', 1, 60),

('video', 'content', 'Video', 'Embedded video player', 
 JSON_OBJECT(
   'src', JSON_OBJECT('type', 'string', 'label', 'Video URL'),
   'youtubeId', JSON_OBJECT('type', 'string', 'label', 'YouTube Video ID'),
   'vimeoId', JSON_OBJECT('type', 'string', 'label', 'Vimeo Video ID'),
   'autoplay', JSON_OBJECT('type', 'boolean', 'label', 'Autoplay', 'default', false),
   'loop', JSON_OBJECT('type', 'boolean', 'label', 'Loop', 'default', false),
   'muted', JSON_OBJECT('type', 'boolean', 'label', 'Muted', 'default', false),
   'controls', JSON_OBJECT('type', 'boolean', 'label', 'Show Controls', 'default', true),
   'aspectRatio', JSON_OBJECT('type', 'string', 'label', 'Aspect Ratio', 'enum', JSON_ARRAY('16:9', '4:3', '1:1', '21:9'), 'default', '16:9')
 ),
 NULL,
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'video', 1, 61),

('gallery', 'content', 'Image Gallery', 'Image gallery with lightbox', 
 JSON_OBJECT(
   'images', JSON_OBJECT('type', 'array', 'label', 'Images', 'itemSchema', JSON_OBJECT('src', JSON_OBJECT('type', 'string'), 'alt', JSON_OBJECT('type', 'string'), 'caption', JSON_OBJECT('type', 'string'))),
   'columns', JSON_OBJECT('type', 'number', 'label', 'Columns', 'min', 1, 'max', 6, 'default', 3),
   'gap', JSON_OBJECT('type', 'number', 'label', 'Gap', 'min', 0, 'max', 50, 'default', 16),
   'enableLightbox', JSON_OBJECT('type', 'boolean', 'label', 'Enable Lightbox', 'default', true)
 ),
 JSON_OBJECT(
   'borderRadius', JSON_OBJECT('type', 'number', 'label', 'Border Radius', 'min', 0, 'max', 50)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'images', 1, 62),

('social-links', 'content', 'Social Links', 'Social media links', 
 JSON_OBJECT(
   'links', JSON_OBJECT('type', 'array', 'label', 'Social Links', 'itemSchema', JSON_OBJECT('platform', JSON_OBJECT('type', 'string', 'enum', JSON_ARRAY('facebook', 'instagram', 'twitter', 'linkedin', 'youtube', 'tiktok')), 'url', JSON_OBJECT('type', 'string'))),
   'layout', JSON_OBJECT('type', 'string', 'label', 'Layout', 'enum', JSON_ARRAY('horizontal', 'vertical'), 'default', 'horizontal'),
   'size', JSON_OBJECT('type', 'string', 'label', 'Icon Size', 'enum', JSON_ARRAY('sm', 'md', 'lg'), 'default', 'md'),
   'showLabels', JSON_OBJECT('type', 'boolean', 'label', 'Show Labels', 'default', false)
 ),
 JSON_OBJECT(
   'iconColor', JSON_OBJECT('type', 'color', 'label', 'Icon Color'),
   'gap', JSON_OBJECT('type', 'number', 'label', 'Gap', 'min', 0, 'max', 50, 'default', 16)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'share-2', 1, 63),

('faq', 'content', 'FAQ Accordion', 'Frequently asked questions accordion', 
 JSON_OBJECT(
   'items', JSON_OBJECT('type', 'array', 'label', 'FAQ Items', 'itemSchema', JSON_OBJECT('question', JSON_OBJECT('type', 'string'), 'answer', JSON_OBJECT('type', 'string'))),
   'allowMultiple', JSON_OBJECT('type', 'boolean', 'label', 'Allow Multiple Open', 'default', false)
 ),
 JSON_OBJECT(
   'borderColor', JSON_OBJECT('type', 'color', 'label', 'Border Color'),
   'borderRadius', JSON_OBJECT('type', 'number', 'label', 'Border Radius', 'min', 0, 'max', 50)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'help-circle', 1, 64),

('cta-banner', 'content', 'CTA Banner', 'Call-to-action banner', 
 JSON_OBJECT(
   'title', JSON_OBJECT('type', 'string', 'label', 'Title', 'required', true),
   'description', JSON_OBJECT('type', 'string', 'label', 'Description'),
   'buttonText', JSON_OBJECT('type', 'string', 'label', 'Button Text'),
   'buttonHref', JSON_OBJECT('type', 'string', 'label', 'Button Link'),
   'backgroundImage', JSON_OBJECT('type', 'string', 'label', 'Background Image URL'),
   'align', JSON_OBJECT('type', 'string', 'label', 'Alignment', 'enum', JSON_ARRAY('left', 'center', 'right'), 'default', 'center')
 ),
 JSON_OBJECT(
   'backgroundColor', JSON_OBJECT('type', 'color', 'label', 'Background Color'),
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'paddingTop', JSON_OBJECT('type', 'number', 'label', 'Padding Top', 'min', 0, 'max', 200),
   'paddingBottom', JSON_OBJECT('type', 'number', 'label', 'Padding Bottom', 'min', 0, 'max', 200)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('*')),
 'megaphone', 1, 65),

('logo', 'content', 'Logo', 'Site logo image', 
 JSON_OBJECT(
   'src', JSON_OBJECT('type', 'string', 'label', 'Logo URL', 'required', true),
   'alt', JSON_OBJECT('type', 'string', 'label', 'Alt Text'),
   'href', JSON_OBJECT('type', 'string', 'label', 'Link URL', 'default', '/'),
   'maxWidth', JSON_OBJECT('type', 'number', 'label', 'Max Width (px)', 'min', 50, 'max', 500, 'default', 200)
 ),
 NULL,
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('header', 'footer')),
 'image', 1, 66),

('nav-menu', 'content', 'Navigation Menu', 'Navigation menu links', 
 JSON_OBJECT(
   'items', JSON_OBJECT('type', 'array', 'label', 'Menu Items', 'itemSchema', JSON_OBJECT('label', JSON_OBJECT('type', 'string'), 'href', JSON_OBJECT('type', 'string'), 'target', JSON_OBJECT('type', 'string'))),
   'style', JSON_OBJECT('type', 'string', 'label', 'Style', 'enum', JSON_ARRAY('horizontal', 'vertical', 'dropdown'), 'default', 'horizontal')
 ),
 JSON_OBJECT(
   'textColor', JSON_OBJECT('type', 'color', 'label', 'Text Color'),
   'activeColor', JSON_OBJECT('type', 'color', 'label', 'Active Color'),
   'gap', JSON_OBJECT('type', 'number', 'label', 'Gap', 'min', 0, 'max', 50, 'default', 24)
 ),
 NULL,
 JSON_OBJECT('allowedChildren', JSON_ARRAY(), 'allowedParents', JSON_ARRAY('header', 'footer')),
 'menu', 1, 67);

-- Update auto-increment for component registry
ALTER TABLE site_builder_component_registry AUTO_INCREMENT = 100;
